// Package resolverwire encodes one explicit classic DNS question and validates
// bounded UDP replies. It opens no sockets, resolves no names, grants no consent,
// and follows no referrals or aliases onto the network.
package resolverwire

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"strings"

	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"golang.org/x/net/dns/dnsmessage"
)

const (
	MaxReplyBytes = 512 // Classic DNS/UDP; no EDNS advertisement or TCP fallback.
	MaxRecords    = 32  // Across answer, authority and additional sections.
	MaxAliases    = 8   // In-message CNAME traversal only.
)

var (
	ErrQuestion = errors.New("resolver question is invalid")
	ErrResponse = errors.New("resolver response is unmatched, malformed or outside bounds")
)

// Query pins wire identity and the selected numeric endpoint. It is not a scope
// authorization or approval ticket. A future sender must generate an unpredictable
// ID, bind and revalidate source/interface/route, and enforce its admitted budget.
// Never log Query or its packet; the selected name can be private household data.
type Query struct {
	endpoint netip.AddrPort
	name     dnsmessage.Name
	kind     dnsmessage.Type
	id       uint16
	packet   []byte
}

// NewQuery accepts an explicit fully-qualified ASCII hostname and A/AAAA type.
// It never appends a search domain, performs IDNA conversion, chooses a resolver,
// or calls host DNS. Endpoint scope authorization belongs to the future caller.
func NewQuery(endpoint netip.AddrPort, name string, kind networkquality.DNSQueryType, id uint16) (Query, error) {
	addr := endpoint.Addr()
	if !endpoint.IsValid() || endpoint.Port() != 53 || addr.IsUnspecified() || addr.IsMulticast() || addr.Is4In6() || addr.Zone() != "" || addr.IsLinkLocalUnicast() || !validHostname(name) {
		return Query{}, ErrQuestion
	}
	typ := dnsmessage.TypeA
	switch kind {
	case networkquality.DNSQueryA:
	case networkquality.DNSQueryAAAA:
		typ = dnsmessage.TypeAAAA
	default:
		return Query{}, ErrQuestion
	}
	n, err := dnsmessage.NewName(strings.ToLower(name))
	if err != nil {
		return Query{}, ErrQuestion
	}
	packet, err := (&dnsmessage.Message{Header: dnsmessage.Header{ID: id, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: n, Type: typ, Class: dnsmessage.ClassINET}}}).Pack()
	if err != nil || len(packet) > MaxReplyBytes {
		return Query{}, ErrQuestion
	}
	return Query{endpoint: endpoint, name: n, kind: typ, id: id, packet: packet}, nil
}

func validHostname(name string) bool {
	if len(name) < 2 || len(name) > 254 || !strings.HasSuffix(name, ".") {
		return false
	}
	for _, label := range strings.Split(name[:len(name)-1], ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range []byte(label) {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// Bytes returns an owned copy, keeping subsequent response matching immutable.
func (q Query) Bytes() []byte { return append([]byte(nil), q.packet...) }

// MatchResponse requires kernel-reported peer identity from the bound socket;
// supplying that identity here does not cryptographically authenticate a reply.
// No raw name, address answer, packet, transaction ID or parser error is returned.
func (q Query) MatchResponse(peer netip.AddrPort, packet []byte) (networkquality.DNSReply, error) {
	invalid := func() (networkquality.DNSReply, error) { return networkquality.DNSReply{}, ErrResponse }
	if len(q.packet) == 0 || peer != q.endpoint || len(packet) < 12 || len(packet) > MaxReplyBytes {
		return invalid()
	}
	flags := binary.BigEndian.Uint16(packet[2:4])
	if binary.BigEndian.Uint16(packet[:2]) != q.id || flags&0x8000 == 0 || flags&0x7800 != 0 || flags&0x0040 != 0 || flags&0x0100 == 0 || binary.BigEndian.Uint16(packet[4:6]) != 1 {
		return invalid()
	}
	total := int(binary.BigEndian.Uint16(packet[6:8])) + int(binary.BigEndian.Uint16(packet[8:10])) + int(binary.BigEndian.Uint16(packet[10:12]))
	if total > MaxRecords {
		return invalid()
	}
	// Even truncated responses must carry a completely framed question.
	questionEnd, err := nameEnd(packet, 12, len(packet), make(map[int]bool))
	if err != nil || questionEnd+4 > len(packet) {
		return invalid()
	}
	var parser dnsmessage.Parser
	header, err := parser.Start(packet)
	if err != nil {
		return invalid()
	}
	question, err := parser.Question()
	if err != nil || question.Type != q.kind || question.Class != dnsmessage.ClassINET || !strings.EqualFold(question.Name.String(), q.name.String()) {
		return invalid()
	}
	if _, err := parser.Question(); err != dnsmessage.ErrSectionDone {
		return invalid()
	}
	// A TC reply can end partway through its RR data. Only its matched header and
	// complete question are evidence; never inspect its partial answer or fallback.
	if header.Truncated {
		return networkquality.DNSReply{RCode: int(header.RCode), Truncated: true}, nil
	}
	if err := validateFraming(packet, total); err != nil {
		return invalid()
	}
	sections := [3][]dnsmessage.Resource{}
	headers := []func() (dnsmessage.ResourceHeader, error){parser.AnswerHeader, parser.AuthorityHeader, parser.AdditionalHeader}
	skips := []func() error{parser.SkipAnswer, parser.SkipAuthority, parser.SkipAdditional}
	for section, read := range headers {
		for {
			hdr, err := read()
			if err == dnsmessage.ErrSectionDone {
				break
			}
			if err != nil || hdr.Class != dnsmessage.ClassINET || hdr.Type == dnsmessage.TypeOPT {
				return invalid()
			}
			resource := dnsmessage.Resource{Header: hdr}
			switch hdr.Type {
			case dnsmessage.TypeA:
				body, e := parser.AResource()
				err = e
				resource.Body = &body
			case dnsmessage.TypeAAAA:
				body, e := parser.AAAAResource()
				err = e
				resource.Body = &body
			case dnsmessage.TypeCNAME:
				body, e := parser.CNAMEResource()
				err = e
				resource.Body = &body
			case dnsmessage.TypeNS:
				body, e := parser.NSResource()
				err = e
				resource.Body = &body
			case dnsmessage.TypeSOA:
				body, e := parser.SOAResource()
				err = e
				resource.Body = &body
			default:
				err = skips[section]() // Bounded opaque data is never interpreted as an answer.
			}
			if err != nil {
				return invalid()
			}
			sections[section] = append(sections[section], resource)
		}
	}
	reply := networkquality.DNSReply{RCode: int(header.RCode)}
	if reply.RCode == 0 {
		reply.Answer = classify(q, sections[0], sections[1])
	}
	return reply, nil
}
