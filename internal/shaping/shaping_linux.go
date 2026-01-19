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
	Tag           string
	IPs           []string
	UploadLimit   string
	DownloadLimit string
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
	if s.cfg.Interface == "" {
		return errors.New("shaping.interface is required")
	}
	if s.cfg.IFBDevice == "" {
		s.cfg.IFBDevice = config.DefaultShapingIFB
	}
	if s.cfg.AggregateLimit == "" {
		s.cfg.AggregateLimit = config.DefaultAggregateLimit
	}

	aggBits, err := config.ParseBandwidth(s.cfg.AggregateLimit)
	if err != nil {
		return fmt.Errorf("shaping.aggregate_limit: %w", err)
	}
	if aggBits == 0 {
		aggBits = defaultAggregateBandwidthBits
	}
	s.cfg.AggregateLimitBits = aggBits

	dev, err := netlink.LinkByName(s.cfg.Interface)
	if err != nil {
		return fmt.Errorf("interface %s not found: %w", s.cfg.Interface, err)
	}
	ifb, err := ensureIFB(s.cfg.IFBDevice)
	if err != nil {
		return fmt.Errorf("ensure IFB %s: %w", s.cfg.IFBDevice, err)
	}

	mtu := dev.Attrs().MTU
	hz := float64(netlink.Hz())

	egressPortClasses, ingressPortClasses, err := buildPortClasses(s.listeners, mtu, hz)
	if err != nil {
		return err
	}
	egressIPClasses, ingressIPClasses, err := buildIPClasses(s.upstreams, mtu, hz)
	if err != nil {
		return err
	}

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
	if err := setupHTBWithPortClasses(dev, s.cfg.AggregateLimitBits, egressPortClasses); err != nil {
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
	if err := setupHTBWithPortClasses(ifb, s.cfg.AggregateLimitBits, ingressPortClasses); err != nil {
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
	if s.cfg.Interface == "" {
		return nil
	}
	dev, err := netlink.LinkByName(s.cfg.Interface)
	if err != nil {
		return fmt.Errorf("cleanup interface %s: %w", s.cfg.Interface, err)
	}
	if err := clearQdiscs(dev); err != nil {
		return fmt.Errorf("cleanup qdiscs on %s: %w", dev.Attrs().Name, err)
	}
	if s.cfg.IFBDevice == "" {
		return nil
	}
	ifb, err := netlink.LinkByName(s.cfg.IFBDevice)
	if err != nil {
		return nil
	}
	if err := clearQdiscs(ifb); err != nil {
		return fmt.Errorf("cleanup qdiscs on %s: %w", ifb.Attrs().Name, err)
	}
	return nil
}

type limitConfig struct {
	rateBits    uint64
	ceilBits    uint64
	burstBytes  uint32
	cburstBytes uint32
}

func parseLimitConfig(limit string, mtu int, hz float64) (*limitConfig, error) {
	limit = strings.TrimSpace(limit)
	if limit == "" {
		return nil, nil
	}
	rateBits, err := config.ParseBandwidth(limit)
	if err != nil {
		return nil, fmt.Errorf("invalid limit: %w", err)
	}
	if rateBits == 0 {
		return nil, fmt.Errorf("limit must be > 0")
	}
	rateBytes := rateBits / 8
	burstBytes := uint32(float64(rateBytes)/hz + float64(mtu))
	return &limitConfig{
		rateBits:    rateBits,
		ceilBits:    rateBits,
		burstBytes:  burstBytes,
		cburstBytes: burstBytes,
	}, nil
}

// buildPortClasses converts config rules to portClass slices for egress and ingress.
func buildPortClasses(listeners []config.ListenerConfig, mtu int, hz float64) (egress []portClass, ingress []portClass, err error) {
	// Use minor class IDs starting from 20, incrementing for each rule.
	// TCP rules get even minors (20, 22, 24...), UDP rules get odd minors (21, 23, 25...).
	tcpMinor := uint16(20)
	udpMinor := uint16(21)
	tcpFilterHandle := uint32(0x10)
	udpFilterHandle := uint32(0x20)

	for _, ln := range listeners {
		if ln.Shaping == nil {
			continue
		}
		var proto int
		var classMinorEgress, classMinorIngress uint16
		var filterHandleEgress, filterHandleIngress uint32

		protoName := strings.ToLower(strings.TrimSpace(ln.Protocol))
		if protoName == "tcp" {
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

		port := uint16(ln.BindPort)
		uploadCfg, err := parseLimitConfig(ln.Shaping.UploadLimit, mtu, hz)
		if err != nil {
			return nil, nil, fmt.Errorf("listener %s:%d upload_limit: %w", ln.Protocol, ln.BindPort, err)
		}
		downloadCfg, err := parseLimitConfig(ln.Shaping.DownloadLimit, mtu, hz)
		if err != nil {
			return nil, nil, fmt.Errorf("listener %s:%d download_limit: %w", ln.Protocol, ln.BindPort, err)
		}
		if uploadCfg != nil {
			egress = append(egress, portClass{
				proto:        proto,
				port:         port,
				matchSrcPort: true,
				rateBits:     uploadCfg.rateBits,
				ceilBits:     uploadCfg.ceilBits,
				burstBytes:   uploadCfg.burstBytes,
				cburstBytes:  uploadCfg.cburstBytes,
				classMinor:   classMinorEgress,
				filterHandle: filterHandleEgress,
			})
		}

		if downloadCfg != nil {
			ingress = append(ingress, portClass{
				proto:        proto,
				port:         port,
				matchSrcPort: false,
				rateBits:     downloadCfg.rateBits,
				ceilBits:     downloadCfg.ceilBits,
				burstBytes:   downloadCfg.burstBytes,
				cburstBytes:  downloadCfg.cburstBytes,
				classMinor:   classMinorIngress,
				filterHandle: filterHandleIngress,
			})
		}
	}

	return egress, ingress, nil
}

// buildIPClasses converts upstream shaping configs to ipClass slices for egress and ingress.
// Egress: match destination IP (traffic TO the upstream).
// Ingress (on IFB): match source IP (traffic FROM the upstream).
func buildIPClasses(upstreams []UpstreamShapingEntry, mtu int, hz float64) (egress []ipClass, ingress []ipClass, err error) {
	// Use minor class IDs starting from 200, incrementing for each IP.
	// This avoids collision with port classes (20-199).
	classMinor := uint16(200)
	filterHandle := uint32(0x200)

	for _, up := range upstreams {
		if strings.TrimSpace(up.UploadLimit) == "" && strings.TrimSpace(up.DownloadLimit) == "" {
			continue
		}
		uploadCfg, err := parseLimitConfig(up.UploadLimit, mtu, hz)
		if err != nil {
			return nil, nil, fmt.Errorf("upstream %s upload_limit: %w", up.Tag, err)
		}
		downloadCfg, err := parseLimitConfig(up.DownloadLimit, mtu, hz)
		if err != nil {
			return nil, nil, fmt.Errorf("upstream %s download_limit: %w", up.Tag, err)
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

			if uploadCfg != nil {
				egress = append(egress, ipClass{
					ip:           ip,
					ipMask:       mask,
					isIPv6:       isIPv6,
					isEgress:     true,
					rateBits:     uploadCfg.rateBits,
					ceilBits:     uploadCfg.ceilBits,
					burstBytes:   uploadCfg.burstBytes,
					cburstBytes:  uploadCfg.cburstBytes,
					classMinor:   classMinor,
					filterHandle: filterHandle,
				})
				classMinor++
				filterHandle++
			}

			if downloadCfg != nil {
				ingress = append(ingress, ipClass{
					ip:           ip,
					ipMask:       mask,
					isIPv6:       isIPv6,
					isEgress:     false,
					rateBits:     downloadCfg.rateBits,
					ceilBits:     downloadCfg.ceilBits,
					burstBytes:   downloadCfg.burstBytes,
					cburstBytes:  downloadCfg.cburstBytes,
					classMinor:   classMinor + 100, // offset for ingress classes
					filterHandle: filterHandle + 0x100,
				})
				classMinor++
				filterHandle++
			}
		}
	}

	return egress, ingress, nil
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
