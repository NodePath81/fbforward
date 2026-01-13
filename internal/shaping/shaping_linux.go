//go:build linux

package shaping

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/util"
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

// UpstreamShapingEntry holds upstream config with resolved IPs for shaping.
type UpstreamShapingEntry struct {
	Tag     string
	IPs     []string
	Ingress *config.BandwidthConfig
	Egress  *config.BandwidthConfig
}

type TrafficShaper struct {
	cfg       config.ShapingConfig
	listeners []config.ListenerConfig
	upstreams []UpstreamShapingEntry
	logger    util.Logger
	mu        sync.Mutex
}

func NewTrafficShaper(cfg config.ShapingConfig, listeners []config.ListenerConfig, upstreams []UpstreamShapingEntry, logger util.Logger) *TrafficShaper {
	return &TrafficShaper{
		cfg:       cfg,
		listeners: listeners,
		upstreams: upstreams,
		logger:    logger,
	}
}

func (s *TrafficShaper) Apply() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyLocked()
}

func (s *TrafficShaper) UpdateUpstreams(upstreams []UpstreamShapingEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upstreams = upstreams
	return s.applyLocked()
}

func (s *TrafficShaper) applyLocked() error {
	if !s.cfg.Enabled {
		return nil
	}
	if s.cfg.Device == "" {
		return errors.New("shaping.device is required")
	}
	if s.cfg.IFB == "" {
		s.cfg.IFB = config.DefaultShapingIFB
	}
	if s.cfg.AggregateBandwidth == "" {
		s.cfg.AggregateBandwidth = config.DefaultAggregateBandwidth
	}

	aggBits, err := config.ParseBandwidth(s.cfg.AggregateBandwidth)
	if err != nil {
		return fmt.Errorf("shaping.aggregate_bandwidth: %w", err)
	}
	if aggBits == 0 {
		aggBits = defaultAggregateBandwidthBits
	}
	s.cfg.AggregateBandwidthBits = aggBits

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

	// Parse listener bandwidth configs
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

	// Parse upstream bandwidth configs
	for i := range s.upstreams {
		up := &s.upstreams[i]
		if up.Ingress != nil {
			if err := parseBandwidthConfig(up.Ingress, mtu, hz); err != nil {
				return fmt.Errorf("upstream %s ingress: %w", up.Tag, err)
			}
		}
		if up.Egress != nil {
			if err := parseBandwidthConfig(up.Egress, mtu, hz); err != nil {
				return fmt.Errorf("upstream %s egress: %w", up.Tag, err)
			}
		}
	}

	egressPortClasses, ingressPortClasses := buildPortClasses(s.listeners)
	egressIPClasses, ingressIPClasses := buildIPClasses(s.upstreams)

	if s.logger != nil {
		s.logger.Info("applying traffic shaping",
			"device", dev.Attrs().Name,
			"ifb", ifb.Attrs().Name,
			"egress_port_rules", len(egressPortClasses),
			"ingress_port_rules", len(ingressPortClasses),
			"egress_ip_rules", len(egressIPClasses),
			"ingress_ip_rules", len(ingressIPClasses))
	}

	// Deterministic apply: clear root+ingress qdiscs on dev and root qdisc on ifb, then rebuild.
	if err := clearQdiscs(dev); err != nil {
		return fmt.Errorf("clear qdiscs on %s: %w", dev.Attrs().Name, err)
	}
	if err := clearQdiscs(ifb); err != nil {
		return fmt.Errorf("clear qdiscs on %s: %w", ifb.Attrs().Name, err)
	}

	// 1) Egress shaping on dev (port-based: match src port; IP-based: match dst IP).
	if err := setupHTBWithPortClasses(dev, s.cfg.AggregateBandwidthBits, egressPortClasses); err != nil {
		return fmt.Errorf("setup egress HTB on %s: %w", dev.Attrs().Name, err)
	}
	if err := addIPClassesToHTB(dev, egressIPClasses, s.logger); err != nil {
		return fmt.Errorf("add egress IP classes on %s: %w", dev.Attrs().Name, err)
	}

	// 2) Ingress redirect dev -> ifb.
	if err := setupIngressRedirectToIFB(dev, ifb); err != nil {
		return fmt.Errorf("setup ingress redirect %s -> %s: %w", dev.Attrs().Name, ifb.Attrs().Name, err)
	}

	// 3) Ingress shaping on IFB (port-based: match dest port; IP-based: match src IP).
	if err := setupHTBWithPortClasses(ifb, s.cfg.AggregateBandwidthBits, ingressPortClasses); err != nil {
		return fmt.Errorf("setup ingress HTB on %s: %w", ifb.Attrs().Name, err)
	}
	if err := addIPClassesToHTB(ifb, ingressIPClasses, s.logger); err != nil {
		return fmt.Errorf("add ingress IP classes on %s: %w", ifb.Attrs().Name, err)
	}

	if s.logger != nil {
		s.logger.Info("traffic shaping applied", "device", dev.Attrs().Name, "ifb", ifb.Attrs().Name)
	}
	return nil
}

func (s *TrafficShaper) Cleanup() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanupLocked()
}

func (s *TrafficShaper) cleanupLocked() error {
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
func parseBandwidthConfig(bc *config.BandwidthConfig, mtu int, hz float64) error {
	if bc == nil {
		return nil
	}

	rateBits, err := config.ParseBandwidth(bc.Rate)
	if err != nil {
		return fmt.Errorf("invalid rate: %w", err)
	}
	if rateBits == 0 {
		return fmt.Errorf("rate is required")
	}
	bc.RateBits = rateBits

	if bc.Ceil == "" {
		bc.CeilBits = rateBits
	} else {
		ceilBits, err := config.ParseBandwidth(bc.Ceil)
		if err != nil {
			return fmt.Errorf("invalid ceil: %w", err)
		}
		if ceilBits == 0 {
			bc.CeilBits = rateBits
		} else {
			bc.CeilBits = ceilBits
		}
	}

	if bc.CeilBits < bc.RateBits {
		return fmt.Errorf("ceil (%d) must be >= rate (%d)", bc.CeilBits, bc.RateBits)
	}

	rateBytes := bc.RateBits / 8
	ceilBytes := bc.CeilBits / 8

	if bc.Burst == "" {
		bc.BurstBytes = uint32(float64(rateBytes)/hz + float64(mtu))
	} else {
		burstBytes, err := config.ParseSize(bc.Burst)
		if err != nil {
			return fmt.Errorf("invalid burst: %w", err)
		}
		bc.BurstBytes = burstBytes
	}

	if bc.Cburst == "" {
		bc.CburstBytes = uint32(float64(ceilBytes)/hz + float64(mtu))
	} else {
		cburstBytes, err := config.ParseSize(bc.Cburst)
		if err != nil {
			return fmt.Errorf("invalid cburst: %w", err)
		}
		bc.CburstBytes = cburstBytes
	}

	return nil
}

// buildPortClasses converts config rules to portClass slices for egress and ingress.
func buildPortClasses(listeners []config.ListenerConfig) (egress []portClass, ingress []portClass) {
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
		if ln.Egress != nil && ln.Egress.RateBits > 0 {
			egress = append(egress, portClass{
				proto:        proto,
				port:         port,
				matchSrcPort: true,
				rateBits:     ln.Egress.RateBits,
				ceilBits:     ln.Egress.CeilBits,
				burstBytes:   ln.Egress.BurstBytes,
				cburstBytes:  ln.Egress.CburstBytes,
				classMinor:   classMinorEgress,
				filterHandle: filterHandleEgress,
			})
		}

		if ln.Ingress != nil && ln.Ingress.RateBits > 0 {
			ingress = append(ingress, portClass{
				proto:        proto,
				port:         port,
				matchSrcPort: false,
				rateBits:     ln.Ingress.RateBits,
				ceilBits:     ln.Ingress.CeilBits,
				burstBytes:   ln.Ingress.BurstBytes,
				cburstBytes:  ln.Ingress.CburstBytes,
				classMinor:   classMinorIngress,
				filterHandle: filterHandleIngress,
			})
		}
	}

	return egress, ingress
}

// buildIPClasses converts upstream shaping configs to ipClass slices for egress and ingress.
// Egress: match destination IP (traffic TO the upstream).
// Ingress (on IFB): match source IP (traffic FROM the upstream).
func buildIPClasses(upstreams []UpstreamShapingEntry) (egress []ipClass, ingress []ipClass) {
	// Use minor class IDs starting from 200, incrementing for each IP.
	// This avoids collision with port classes (20-199).
	classMinor := uint16(200)
	filterHandle := uint32(0x200)

	for _, up := range upstreams {
		if up.Ingress == nil && up.Egress == nil {
			continue
		}

		for _, ipStr := range up.IPs {
			ip := net.ParseIP(ipStr)
			if ip == nil {
				continue
			}

			// Determine mask and version based on IP type
			var mask net.IPMask
			var isIPv6 bool
			if ip4 := ip.To4(); ip4 != nil {
				ip = ip4
				mask = net.CIDRMask(32, 32)
				isIPv6 = false
			} else {
				mask = net.CIDRMask(128, 128)
				isIPv6 = true
			}

			if up.Egress != nil && up.Egress.RateBits > 0 {
				egress = append(egress, ipClass{
					ip:           ip,
					ipMask:       mask,
					isIPv6:       isIPv6,
					isEgress:     true,
					rateBits:     up.Egress.RateBits,
					ceilBits:     up.Egress.CeilBits,
					burstBytes:   up.Egress.BurstBytes,
					cburstBytes:  up.Egress.CburstBytes,
					classMinor:   classMinor,
					filterHandle: filterHandle,
				})
				classMinor++
				filterHandle++
			}

			if up.Ingress != nil && up.Ingress.RateBits > 0 {
				ingress = append(ingress, ipClass{
					ip:           ip,
					ipMask:       mask,
					isIPv6:       isIPv6,
					isEgress:     false,
					rateBits:     up.Ingress.RateBits,
					ceilBits:     up.Ingress.CeilBits,
					burstBytes:   up.Ingress.BurstBytes,
					cburstBytes:  up.Ingress.CburstBytes,
					classMinor:   classMinor + 100, // offset for ingress classes
					filterHandle: filterHandle + 0x100,
				})
				classMinor++
				filterHandle++
			}
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

// ipClass represents an IP-based traffic class for upstream shaping.
type ipClass struct {
	ip           net.IP     // IP address to match
	ipMask       net.IPMask // mask (typically /32 for single IP)
	isIPv6       bool       // true for IPv6, false for IPv4
	isEgress     bool       // true: match dst IP (egress); false: match src IP (ingress on IFB)
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

	// Redirect IPv4 traffic to IFB
	mirredV4 := netlink.NewMirredAction(ifb.Attrs().Index)
	mirredV4.MirredAction = netlink.TCA_EGRESS_REDIR

	fV4 := &netlink.MatchAll{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: devIdx,
			Parent:    netlink.HANDLE_INGRESS,
			Priority:  1,
			Protocol:  unix.ETH_P_IP,
			Handle:    filterHandleRedirect,
		},
		Actions: []netlink.Action{mirredV4},
	}
	if err := netlink.FilterReplace(fV4); err != nil {
		return fmt.Errorf("FilterReplace(matchall mirred redirect IPv4): %w", err)
	}

	// Redirect IPv6 traffic to IFB
	mirredV6 := netlink.NewMirredAction(ifb.Attrs().Index)
	mirredV6.MirredAction = netlink.TCA_EGRESS_REDIR

	fV6 := &netlink.MatchAll{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: devIdx,
			Parent:    netlink.HANDLE_INGRESS,
			Priority:  2,
			Protocol:  unix.ETH_P_IPV6,
			Handle:    filterHandleRedirect + 1, // Different handle for IPv6 filter
		},
		Actions: []netlink.Action{mirredV6},
	}
	if err := netlink.FilterReplace(fV6); err != nil {
		return fmt.Errorf("FilterReplace(matchall mirred redirect IPv6): %w", err)
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

// addIPClassesToHTB adds IP-based traffic classes and flower filters to an existing HTB qdisc.
// This should be called after setupHTBWithPortClasses to add upstream shaping rules.
func addIPClassesToHTB(link netlink.Link, ipcs []ipClass, logger util.Logger) error {
	if len(ipcs) == 0 {
		return nil
	}

	idx := link.Attrs().Index
	rootQdiscHandle := netlink.MakeHandle(handleMajorHTB, 0)
	rootClassID := netlink.MakeHandle(handleMajorHTB, classRootMinor)

	for _, ic := range ipcs {
		classID := netlink.MakeHandle(handleMajorHTB, ic.classMinor)
		rateBytes := bitsToBytesPerSec(ic.rateBits)
		ceilBytes := bitsToBytesPerSec(ic.ceilBits)

		buffer := netlink.Xmittime(rateBytes, ic.burstBytes)
		cbuffer := netlink.Xmittime(ceilBytes, ic.cburstBytes)

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
			return fmt.Errorf("add/replace class 1:%d: %w", ic.classMinor, err)
		}

		if err := ensureFqCodel(idx, classID, netlink.MakeHandle(ic.classMinor, 0)); err != nil {
			return fmt.Errorf("fq_codel under class 1:%d: %w", ic.classMinor, err)
		}

		// Build flower filter with IP match criteria
		// Use appropriate protocol based on IP version
		var ethProto uint16
		if ic.isIPv6 {
			ethProto = unix.ETH_P_IPV6
		} else {
			ethProto = unix.ETH_P_IP
		}

		flower := &netlink.Flower{
			FilterAttrs: netlink.FilterAttrs{
				LinkIndex: idx,
				Parent:    rootQdiscHandle,
				Priority:  2, // Lower priority than port-based filters (priority 1)
				Protocol:  ethProto,
				Handle:    ic.filterHandle,
			},
			EthType: ethProto,
			ClassId: classID,
		}

		// Set IP match criteria:
		// For egress: match destination IP (traffic TO the upstream)
		// For ingress (on IFB): match source IP (traffic FROM the upstream)
		if ic.isEgress {
			flower.DestIP = ic.ip
			flower.DestIPMask = ic.ipMask
		} else {
			flower.SrcIP = ic.ip
			flower.SrcIPMask = ic.ipMask
		}

		if err := netlink.FilterReplace(flower); err != nil {
			if ic.isIPv6 && errors.Is(err, unix.EINVAL) {
				if logger != nil {
					logger.Warn("ipv6 flower filter unsupported; skipping ip-based shaping rule",
						"device", link.Attrs().Name,
						"ip", ic.ip.String())
				}
				continue
			}
			return fmt.Errorf("FilterReplace(flower IP class 1:%d): %w", ic.classMinor, err)
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
