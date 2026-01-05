//go:build linux

package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

const (
	handleMajorHTB uint16 = 1

	classRootMinor    uint16 = 1
	classDefaultMinor uint16 = 10

	// stable filter handles (per-interface/per-parent) for deterministic replace
	filterHandleRedirect uint32 = 0x1

	defaultAggregateBandwidthBits uint64 = 1_000_000_000
)

type TrafficShaper struct {
	cfg       ShapingConfig
	listeners []ListenerConfig
	logger    Logger
}

func NewTrafficShaper(cfg ShapingConfig, listeners []ListenerConfig, logger Logger) *TrafficShaper {
	return &TrafficShaper{
		cfg:       cfg,
		listeners: listeners,
		logger:    logger,
	}
}

func (s *TrafficShaper) Apply() error {
	if !s.cfg.Enabled {
		return nil
	}
	if s.cfg.Device == "" {
		return errors.New("shaping.device is required")
	}
	if s.cfg.IFB == "" {
		s.cfg.IFB = defaultShapingIFB
	}
	if s.cfg.AggregateBandwidth == "" {
		s.cfg.AggregateBandwidth = defaultAggregateBandwidth
	}

	aggBits, err := parseBandwidth(s.cfg.AggregateBandwidth)
	if err != nil {
		return fmt.Errorf("shaping.aggregate_bandwidth: %w", err)
	}
	if aggBits == 0 {
		aggBits = defaultAggregateBandwidthBits
	}
	s.cfg.aggregateBandwidthBits = aggBits

	dev, err := netlink.LinkByName(s.cfg.Device)
	if err != nil {
		return fmt.Errorf("device %s not found: %w", s.cfg.Device, err)
	}
	ifb, err := ensureIFB(s.cfg.IFB)
	if err != nil {
		return fmt.Errorf("ensure IFB %s: %w", s.cfg.IFB, err)
	}

	mtu := dev.Attrs().MTU
	hz := float64(netlink.Hz())

	for i := range s.listeners {
		ln := &s.listeners[i]
		ln.Protocol = strings.ToLower(strings.TrimSpace(ln.Protocol))
		if ln.Ingress != nil {
			if err := parseBandwidthConfig(ln.Ingress, mtu, hz); err != nil {
				return fmt.Errorf("listener %s:%d ingress: %w", ln.Protocol, ln.Port, err)
			}
		}
		if ln.Egress != nil {
			if err := parseBandwidthConfig(ln.Egress, mtu, hz); err != nil {
				return fmt.Errorf("listener %s:%d egress: %w", ln.Protocol, ln.Port, err)
			}
		}
	}

	egressClasses, ingressClasses := buildPortClasses(s.listeners)
	if s.logger != nil {
		s.logger.Info("applying traffic shaping", "device", dev.Attrs().Name, "ifb", ifb.Attrs().Name, "egress_rules", len(egressClasses), "ingress_rules", len(ingressClasses))
	}

	// Deterministic apply: clear root+ingress qdiscs on dev and root qdisc on ifb, then rebuild.
	if err := clearQdiscs(dev); err != nil {
		return fmt.Errorf("clear qdiscs on %s: %w", dev.Attrs().Name, err)
	}
	if err := clearQdiscs(ifb); err != nil {
		return fmt.Errorf("clear qdiscs on %s: %w", ifb.Attrs().Name, err)
	}

	// 1) Egress shaping on dev (match src port).
	if err := setupHTBWithPortClasses(dev, s.cfg.aggregateBandwidthBits, egressClasses); err != nil {
		return fmt.Errorf("setup egress HTB on %s: %w", dev.Attrs().Name, err)
	}

	// 2) Ingress redirect dev -> ifb.
	if err := setupIngressRedirectToIFB(dev, ifb); err != nil {
		return fmt.Errorf("setup ingress redirect %s -> %s: %w", dev.Attrs().Name, ifb.Attrs().Name, err)
	}

	// 3) Ingress shaping is done as egress shaping on IFB (match dest port).
	if err := setupHTBWithPortClasses(ifb, s.cfg.aggregateBandwidthBits, ingressClasses); err != nil {
		return fmt.Errorf("setup ingress HTB on %s: %w", ifb.Attrs().Name, err)
	}

	if s.logger != nil {
		s.logger.Info("traffic shaping applied", "device", dev.Attrs().Name, "ifb", ifb.Attrs().Name)
	}
	return nil
}

func (s *TrafficShaper) Cleanup() error {
	if !s.cfg.Enabled {
		return nil
	}
	if s.cfg.Device == "" {
		return nil
	}
	dev, err := netlink.LinkByName(s.cfg.Device)
	if err != nil {
		return fmt.Errorf("cleanup device %s: %w", s.cfg.Device, err)
	}
	if err := clearQdiscs(dev); err != nil {
		return fmt.Errorf("cleanup qdiscs on %s: %w", dev.Attrs().Name, err)
	}
	if s.cfg.IFB == "" {
		return nil
	}
	ifb, err := netlink.LinkByName(s.cfg.IFB)
	if err != nil {
		return nil
	}
	if err := clearQdiscs(ifb); err != nil {
		return fmt.Errorf("cleanup qdiscs on %s: %w", ifb.Attrs().Name, err)
	}
	return nil
}

// parseBandwidthConfig parses and validates a BandwidthConfig.
// mtu and hz are used for calculating default burst values.
func parseBandwidthConfig(bc *BandwidthConfig, mtu int, hz float64) error {
	if bc == nil {
		return nil
	}

	rateBits, err := parseBandwidth(bc.Rate)
	if err != nil {
		return fmt.Errorf("invalid rate: %w", err)
	}
	if rateBits == 0 {
		return fmt.Errorf("rate is required")
	}
	bc.rateBits = rateBits

	if bc.Ceil == "" {
		bc.ceilBits = rateBits
	} else {
		ceilBits, err := parseBandwidth(bc.Ceil)
		if err != nil {
			return fmt.Errorf("invalid ceil: %w", err)
		}
		if ceilBits == 0 {
			bc.ceilBits = rateBits
		} else {
			bc.ceilBits = ceilBits
		}
	}

	if bc.ceilBits < bc.rateBits {
		return fmt.Errorf("ceil (%d) must be >= rate (%d)", bc.ceilBits, bc.rateBits)
	}

	rateBytes := bc.rateBits / 8
	ceilBytes := bc.ceilBits / 8

	if bc.Burst == "" {
		bc.burstBytes = uint32(float64(rateBytes)/hz + float64(mtu))
	} else {
		burstBytes, err := parseSize(bc.Burst)
		if err != nil {
			return fmt.Errorf("invalid burst: %w", err)
		}
		bc.burstBytes = burstBytes
	}

	if bc.Cburst == "" {
		bc.cburstBytes = uint32(float64(ceilBytes)/hz + float64(mtu))
	} else {
		cburstBytes, err := parseSize(bc.Cburst)
		if err != nil {
			return fmt.Errorf("invalid cburst: %w", err)
		}
		bc.cburstBytes = cburstBytes
	}

	return nil
}

// buildPortClasses converts config rules to portClass slices for egress and ingress.
func buildPortClasses(listeners []ListenerConfig) (egress []portClass, ingress []portClass) {
	// Use minor class IDs starting from 20, incrementing for each rule.
	// TCP rules get even minors (20, 22, 24...), UDP rules get odd minors (21, 23, 25...).
	tcpMinor := uint16(20)
	udpMinor := uint16(21)
	tcpFilterHandle := uint32(0x10)
	udpFilterHandle := uint32(0x20)

	for _, ln := range listeners {
		if ln.Ingress == nil && ln.Egress == nil {
			continue
		}
		var proto int
		var classMinorEgress, classMinorIngress uint16
		var filterHandleEgress, filterHandleIngress uint32

		if ln.Protocol == "tcp" {
			proto = unix.IPPROTO_TCP
			classMinorEgress = tcpMinor
			classMinorIngress = tcpMinor + 100
			filterHandleEgress = tcpFilterHandle
			filterHandleIngress = tcpFilterHandle + 0x100
			tcpMinor += 2
			tcpFilterHandle++
		} else {
			proto = unix.IPPROTO_UDP
			classMinorEgress = udpMinor
			classMinorIngress = udpMinor + 100
			filterHandleEgress = udpFilterHandle
			filterHandleIngress = udpFilterHandle + 0x100
			udpMinor += 2
			udpFilterHandle++
		}

		port := uint16(ln.Port)
		if ln.Egress != nil && ln.Egress.rateBits > 0 {
			egress = append(egress, portClass{
				proto:        proto,
				port:         port,
				matchSrcPort: true,
				rateBits:     ln.Egress.rateBits,
				ceilBits:     ln.Egress.ceilBits,
				burstBytes:   ln.Egress.burstBytes,
				cburstBytes:  ln.Egress.cburstBytes,
				classMinor:   classMinorEgress,
				filterHandle: filterHandleEgress,
			})
		}

		if ln.Ingress != nil && ln.Ingress.rateBits > 0 {
			ingress = append(ingress, portClass{
				proto:        proto,
				port:         port,
				matchSrcPort: false,
				rateBits:     ln.Ingress.rateBits,
				ceilBits:     ln.Ingress.ceilBits,
				burstBytes:   ln.Ingress.burstBytes,
				cburstBytes:  ln.Ingress.cburstBytes,
				classMinor:   classMinorIngress,
				filterHandle: filterHandleIngress,
			})
		}
	}

	return egress, ingress
}

type portClass struct {
	proto        int
	port         uint16
	matchSrcPort bool
	rateBits     uint64
	ceilBits     uint64
	burstBytes   uint32
	cburstBytes  uint32
	classMinor   uint16
	filterHandle uint32
}

func ensureIFB(name string) (netlink.Link, error) {
	link, err := netlink.LinkByName(name)
	if err == nil {
		_ = netlink.LinkSetUp(link)
		return link, nil
	}

	la := netlink.NewLinkAttrs()
	la.Name = name
	ifb := &netlink.Ifb{LinkAttrs: la}
	if err := netlink.LinkAdd(ifb); err != nil {
		return nil, fmt.Errorf("LinkAdd(ifb %s): %w (is the ifb kernel module available/loaded?)", name, err)
	}
	if err := netlink.LinkSetUp(ifb); err != nil {
		return nil, fmt.Errorf("LinkSetUp(%s): %w", name, err)
	}
	return ifb, nil
}

func clearQdiscs(link netlink.Link) error {
	qdiscs, err := netlink.QdiscList(link)
	if err != nil {
		return fmt.Errorf("QdiscList: %w", err)
	}
	for _, q := range qdiscs {
		p := q.Attrs().Parent
		// Remove root qdisc and ingress qdisc. This resets existing tc state on the interface.
		if p == netlink.HANDLE_ROOT || p == netlink.HANDLE_INGRESS {
			_ = netlink.QdiscDel(q)
		}
	}
	return nil
}

func setupIngressRedirectToIFB(dev netlink.Link, ifb netlink.Link) error {
	devIdx := dev.Attrs().Index

	ing := &netlink.Ingress{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: devIdx,
			Handle:    netlink.MakeHandle(0xffff, 0),
			Parent:    netlink.HANDLE_INGRESS,
		},
	}
	if err := qdiscReplaceOrAdd(ing); err != nil {
		return fmt.Errorf("add/replace ingress qdisc: %w", err)
	}

	mirred := netlink.NewMirredAction(ifb.Attrs().Index)
	mirred.MirredAction = netlink.TCA_EGRESS_REDIR

	f := &netlink.MatchAll{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: devIdx,
			Parent:    netlink.HANDLE_INGRESS,
			Priority:  1,
			Protocol:  unix.ETH_P_IP,
			Handle:    filterHandleRedirect,
		},
		Actions: []netlink.Action{mirred},
	}
	if err := netlink.FilterReplace(f); err != nil {
		return fmt.Errorf("FilterReplace(matchall mirred redirect): %w", err)
	}
	return nil
}

func setupHTBWithPortClasses(link netlink.Link, aggBits uint64, pcs []portClass) error {
	idx := link.Attrs().Index

	rootQdiscHandle := netlink.MakeHandle(handleMajorHTB, 0)
	rootClassID := netlink.MakeHandle(handleMajorHTB, classRootMinor)
	defaultClassID := netlink.MakeHandle(handleMajorHTB, classDefaultMinor)

	aggBytes := bitsToBytesPerSec(aggBits)

	htb := netlink.NewHtb(netlink.QdiscAttrs{
		LinkIndex: idx,
		Handle:    rootQdiscHandle,
		Parent:    netlink.HANDLE_ROOT,
	})
	htb.Defcls = uint32(classDefaultMinor)
	htb.Rate2Quantum = 100
	if err := qdiscReplaceOrAdd(htb); err != nil {
		return fmt.Errorf("add/replace root htb qdisc: %w", err)
	}

	if err := classReplaceOrAdd(&netlink.HtbClass{
		ClassAttrs: netlink.ClassAttrs{
			LinkIndex: idx,
			Handle:    rootClassID,
			Parent:    rootQdiscHandle,
		},
		Rate: aggBytes,
		Ceil: aggBytes,
	}); err != nil {
		return fmt.Errorf("add/replace root class: %w", err)
	}

	if err := classReplaceOrAdd(&netlink.HtbClass{
		ClassAttrs: netlink.ClassAttrs{
			LinkIndex: idx,
			Handle:    defaultClassID,
			Parent:    rootClassID,
		},
		Rate: aggBytes,
		Ceil: aggBytes,
	}); err != nil {
		return fmt.Errorf("add/replace default class: %w", err)
	}

	if err := ensureFqCodel(idx, defaultClassID, netlink.MakeHandle(classDefaultMinor, 0)); err != nil {
		return fmt.Errorf("fq_codel under default class: %w", err)
	}

	for _, pc := range pcs {
		classID := netlink.MakeHandle(handleMajorHTB, pc.classMinor)
		rateBytes := bitsToBytesPerSec(pc.rateBits)
		ceilBytes := bitsToBytesPerSec(pc.ceilBits)

		buffer := netlink.Xmittime(rateBytes, pc.burstBytes)
		cbuffer := netlink.Xmittime(ceilBytes, pc.cburstBytes)

		if err := classReplaceOrAdd(&netlink.HtbClass{
			ClassAttrs: netlink.ClassAttrs{
				LinkIndex: idx,
				Handle:    classID,
				Parent:    rootClassID,
			},
			Rate:    rateBytes,
			Ceil:    ceilBytes,
			Buffer:  buffer,
			Cbuffer: cbuffer,
		}); err != nil {
			return fmt.Errorf("add/replace class 1:%d: %w", pc.classMinor, err)
		}

		if err := ensureFqCodel(idx, classID, netlink.MakeHandle(pc.classMinor, 0)); err != nil {
			return fmt.Errorf("fq_codel under class 1:%d: %w", pc.classMinor, err)
		}

		ipProto := nl.IPProto(uint8(pc.proto))
		flower := &netlink.Flower{
			FilterAttrs: netlink.FilterAttrs{
				LinkIndex: idx,
				Parent:    rootQdiscHandle,
				Priority:  1,
				Protocol:  unix.ETH_P_IP,
				Handle:    pc.filterHandle,
			},
			EthType: unix.ETH_P_IP,
			IPProto: &ipProto,
			ClassId: classID,
		}

		if pc.matchSrcPort {
			flower.SrcPort = pc.port
		} else {
			flower.DestPort = pc.port
		}

		if err := netlink.FilterReplace(flower); err != nil {
			return fmt.Errorf("FilterReplace(flower class 1:%d): %w", pc.classMinor, err)
		}
	}

	return nil
}

func ensureFqCodel(linkIndex int, parent uint32, handle uint32) error {
	fq := netlink.NewFqCodel(netlink.QdiscAttrs{
		LinkIndex: linkIndex,
		Parent:    parent,
		Handle:    handle,
	})
	return qdiscReplaceOrAdd(fq)
}

func bitsToBytesPerSec(bits uint64) uint64 {
	return bits / 8
}

func qdiscReplaceOrAdd(q netlink.Qdisc) error {
	if err := netlink.QdiscReplace(q); err == nil {
		return nil
	}
	_ = netlink.QdiscDel(q)
	if err := netlink.QdiscAdd(q); err != nil {
		return fmt.Errorf("replace failed, add failed: %w", err)
	}
	return nil
}

func classReplaceOrAdd(c netlink.Class) error {
	if err := netlink.ClassReplace(c); err == nil {
		return nil
	} else {
		replaceErr := err
		_ = netlink.ClassDel(c)
		if err := netlink.ClassAdd(c); err != nil {
			if errors.Is(err, unix.EINVAL) {
				return fmt.Errorf("class add got EINVAL (parent missing/unsupported?): %w (replace err was %v)", err, replaceErr)
			}
			return fmt.Errorf("replace failed, add failed: %w / %v", err, replaceErr)
		}
		return nil
	}
}
