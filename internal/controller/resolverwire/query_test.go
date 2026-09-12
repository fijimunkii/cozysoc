package resolverwire

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"golang.org/x/net/dns/dnsmessage"
)

var peer = netip.MustParseAddrPort("192.0.2.53:53")

func query(t testing.TB) Query {
	t.Helper()
	q, e := NewQuery(peer, "test.example.", nq.DNSQueryA, 1234)
	if e != nil {
		t.Fatal(e)
	}
	return q
}
func name(s string) dnsmessage.Name { return dnsmessage.MustNewName(s) }
func rr(owner string, body dnsmessage.ResourceBody) dnsmessage.Resource {
	return dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: name(owner), Class: dnsmessage.ClassINET}, Body: body}
}
func address(owner string) dnsmessage.Resource {
	return rr(owner, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}})
}
func alias(owner, target string) dnsmessage.Resource {
	return rr(owner, &dnsmessage.CNAMEResource{CNAME: name(target)})
}
func soa(owner string) dnsmessage.Resource {
	return rr(owner, &dnsmessage.SOAResource{NS: name("ns.example."), MBox: name("admin.example.")})
}
func response(q Query) dnsmessage.Message {
	return dnsmessage.Message{Header: dnsmessage.Header{ID: q.id, Response: true, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: q.name, Type: q.kind, Class: dnsmessage.ClassINET}}}
}
func pack(t testing.TB, m dnsmessage.Message) []byte {
	t.Helper()
	b, e := m.Pack()
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestQueryValidationAndOwnership(t *testing.T) {
	q := query(t)
	b := q.Bytes()
	var m dnsmessage.Message
	if e := m.Unpack(b); e != nil {
		t.Fatal(e)
	}
	if len(m.Questions) != 1 || !m.RecursionDesired || m.Response || len(m.Additionals) != 0 {
		t.Fatalf("unexpected query: %+v", m)
	}
	b[0] ^= 1
	if q.Bytes()[0] == b[0] {
		t.Fatal("packet aliases query")
	}
	for _, s := range []string{"", ".", "example", "a..example.", "-a.example.", "a-.example.", "a_b.example.", "é.example.", "https://example.", strings.Repeat("a", 64) + "."} {
		if _, e := NewQuery(peer, s, nq.DNSQueryA, 0); e != ErrQuestion {
			t.Errorf("accepted %q", s)
		}
	}
	max := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61) + "."
	if _, e := NewQuery(peer, max, nq.DNSQueryA, 0); e != nil {
		t.Fatal(e)
	}
	if _, e := NewQuery(peer, "x."+max, nq.DNSQueryA, 0); e != ErrQuestion {
		t.Fatal("accepted oversized name")
	}
	for _, s := range []string{"192.0.2.53:54", "0.0.0.0:53", "224.0.0.1:53", "[::]:53", "[::ffff:192.0.2.53]:53", "[fe80::1%en0]:53"} {
		if _, e := NewQuery(netip.MustParseAddrPort(s), "test.example.", nq.DNSQueryA, 0); e != ErrQuestion {
			t.Errorf("accepted %s", s)
		}
	}
	if _, e := NewQuery(peer, "test.example.", "TXT", 0); e != ErrQuestion {
		t.Fatal("accepted TXT")
	}
	q, e := NewQuery(netip.MustParseAddrPort("[2001:db8::53]:53"), "TEST.Example.", nq.DNSQueryAAAA, 0)
	if e != nil {
		t.Fatal(e)
	}
	m = response(q)
	m.Answers = []dnsmessage.Resource{rr("test.example.", &dnsmessage.AAAAResource{})}
	got, e := q.MatchResponse(q.endpoint, pack(t, m))
	if e != nil || got.Answer != nq.DNSAnswerPresent {
		t.Fatalf("AAAA: %+v %v", got, e)
	}
}

func TestResponseClassification(t *testing.T) {
	cases := []struct {
		name                           string
		answers, authority, additional []dnsmessage.Resource
		want                           nq.DNSAnswerKind
	}{
		{name: "answer", answers: []dnsmessage.Resource{address("test.example.")}, want: nq.DNSAnswerPresent},
		{name: "empty", want: nq.DNSAnswerNoData},
		{name: "negative SOA", authority: []dnsmessage.Resource{soa("example.")}, want: nq.DNSAnswerNoData},
		{name: "unrelated SOA", authority: []dnsmessage.Resource{soa("other.")}, want: nq.DNSAnswerUnclassified},
		{name: "referral", authority: []dnsmessage.Resource{rr("example.", &dnsmessage.NSResource{NS: name("ns.example.")})}, want: nq.DNSAnswerReferral},
		{name: "unrelated answer", answers: []dnsmessage.Resource{address("other.example.")}, want: nq.DNSAnswerUnclassified},
		{name: "wrong type", answers: []dnsmessage.Resource{rr("test.example.", &dnsmessage.AAAAResource{})}, want: nq.DNSAnswerUnclassified},
		{name: "additional only", additional: []dnsmessage.Resource{address("test.example.")}, want: nq.DNSAnswerNoData},
		{name: "alias", answers: []dnsmessage.Resource{alias("test.example.", "other.example."), address("other.example.")}, want: nq.DNSAnswerPresent},
		{name: "alias negative", answers: []dnsmessage.Resource{alias("test.example.", "other.example.")}, authority: []dnsmessage.Resource{soa("example.")}, want: nq.DNSAnswerNoData},
		{name: "unresolved alias", answers: []dnsmessage.Resource{alias("test.example.", "other.example.")}, want: nq.DNSAnswerUnclassified},
		{name: "cycle", answers: []dnsmessage.Resource{alias("test.example.", "other.example."), alias("other.example.", "test.example.")}, want: nq.DNSAnswerUnclassified},
		{name: "conflicting alias", answers: []dnsmessage.Resource{alias("test.example.", "one.example."), alias("test.example.", "two.example.")}, want: nq.DNSAnswerUnclassified},
		{name: "alias and address", answers: []dnsmessage.Resource{alias("test.example.", "other.example."), address("test.example.")}, want: nq.DNSAnswerUnclassified},
		{name: "opaque answer", answers: []dnsmessage.Resource{rr("test.example.", &dnsmessage.UnknownResource{Type: 99, Data: []byte{1, 2, 3}})}, want: nq.DNSAnswerUnclassified},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			q := query(t)
			m := response(q)
			m.Answers = tt.answers
			m.Authorities = tt.authority
			m.Additionals = tt.additional
			got, e := q.MatchResponse(peer, pack(t, m))
			if e != nil || got.Answer != tt.want {
				t.Fatalf("got %+v %v want %s", got, e, tt.want)
			}
		})
	}
	for depth := 8; depth <= 9; depth++ {
		q := query(t)
		m := response(q)
		owner := "test.example."
		for i := 0; i < depth; i++ {
			next := fmt.Sprintf("n%d.example.", i)
			m.Answers = append(m.Answers, alias(owner, next))
			owner = next
		}
		m.Answers = append(m.Answers, address(owner))
		got, e := q.MatchResponse(peer, pack(t, m))
		want := nq.DNSAnswerPresent
		if depth > 8 {
			want = nq.DNSAnswerUnclassified
		}
		if e != nil || got.Answer != want {
			t.Fatalf("depth %d: %+v %v", depth, got, e)
		}
	}
	for code := 1; code <= 15; code++ {
		q := query(t)
		m := response(q)
		m.RCode = dnsmessage.RCode(code)
		got, e := q.MatchResponse(peer, pack(t, m))
		if e != nil || got.RCode != code || got.Answer != "" {
			t.Fatalf("rcode %d: %+v %v", code, got, e)
		}
	}
}

func TestRejectUnmatchedAndMalformed(t *testing.T) {
	q := query(t)
	m := response(q)
	m.Answers = []dnsmessage.Resource{address("test.example.")}
	good := pack(t, m)
	mutations := map[string]func([]byte) []byte{
		"id":             func(b []byte) []byte { b[0] ^= 1; return b },
		"query":          func(b []byte) []byte { b[2] &= 0x7f; return b },
		"opcode":         func(b []byte) []byte { b[2] |= 8; return b },
		"RD":             func(b []byte) []byte { b[2] &= 0xfe; return b },
		"reserved":       func(b []byte) []byte { b[3] |= 0x40; return b },
		"question count": func(b []byte) []byte { b[5] = 2; return b },
		"record count":   func(b []byte) []byte { b[7] = 33; return b },
		"name":           func(b []byte) []byte { b[13] = 'x'; return b },
		"type":           func(b []byte) []byte { b[len(q.packet)-3] = 28; return b },
		"class":          func(b []byte) []byte { b[len(q.packet)-1] = 3; return b },
		"short A":        func(b []byte) []byte { b[len(b)-5] = 3; return b },
		"long A":         func(b []byte) []byte { b[len(b)-5] = 5; return append(b, 0) },
		"trailing":       func(b []byte) []byte { return append(b, 0) },
		"oversized":      func(b []byte) []byte { return append(b, make([]byte, 513-len(b))...) },
		"pointer loop":   func(b []byte) []byte { b[12] = 0xc0; b[13] = 12; return b },
	}
	for label, mut := range mutations {
		t.Run(label, func(t *testing.T) {
			if got, e := q.MatchResponse(peer, mut(append([]byte(nil), good...))); e != ErrResponse || got != (nq.DNSReply{}) {
				t.Fatalf("accepted: %+v %v", got, e)
			}
		})
	}
	for i := 0; i < len(good); i++ {
		if _, e := q.MatchResponse(peer, good[:i]); e != ErrResponse {
			t.Fatalf("accepted prefix %d", i)
		}
	}
	for _, p := range []netip.AddrPort{netip.MustParseAddrPort("192.0.2.54:53"), netip.MustParseAddrPort("192.0.2.53:54"), netip.MustParseAddrPort("[2001:db8::53]:53")} {
		if _, e := q.MatchResponse(p, good); e != ErrResponse {
			t.Fatal("accepted peer")
		}
	}
	if _, e := (Query{}).MatchResponse(peer, good); e != ErrResponse {
		t.Fatal("accepted zero query")
	}
	m = response(q)
	m.Additionals = []dnsmessage.Resource{rr(".", &dnsmessage.OPTResource{})}
	if _, e := q.MatchResponse(peer, pack(t, m)); e != ErrResponse {
		t.Fatal("accepted unsolicited OPT")
	}
}

func TestTruncatedReply(t *testing.T) {
	q := query(t)
	m := response(q)
	m.Truncated = true
	b := pack(t, m)
	binary.BigEndian.PutUint16(b[6:8], 1)
	b = append(b, 0xc0)
	got, e := q.MatchResponse(peer, b)
	if e != nil || !got.Truncated || got.Answer != "" {
		t.Fatalf("%+v %v", got, e)
	}
	if _, e := q.MatchResponse(peer, b[:len(q.packet)-1]); e != ErrResponse {
		t.Fatal("accepted partial question")
	}
}

func FuzzMatchResponse(f *testing.F) {
	q := query(f)
	m := response(q)
	m.Answers = []dnsmessage.Resource{address("test.example.")}
	f.Add(pack(f, m))
	f.Add(q.Bytes())
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		got, e := q.MatchResponse(peer, b)
		if e != nil {
			if e != ErrResponse || got != (nq.DNSReply{}) {
				t.Fatal("unsafe error result")
			}
			return
		}
		if got.RCode < 0 || got.RCode > 15 || ((got.Truncated || got.RCode != 0) && got.Answer != "") {
			t.Fatalf("contradictory result %+v", got)
		}
	})
}

func TestResourceFraming(t *testing.T) {
	q := query(t)
	// Header/question followed by a compressed owner and explicitly sized RDATA.
	wire := func(kind dnsmessage.Type, body []byte) []byte {
		m := response(q)
		b := pack(t, m)
		b[7] = 1
		b = append(b, 0xc0, 12, byte(kind>>8), byte(kind), 0, 1, 0, 0, 0, 0, byte(len(body)>>8), byte(len(body)))
		return append(b, body...)
	}
	for _, tt := range []struct {
		label string
		kind  dnsmessage.Type
		body  []byte
	}{
		{"short AAAA", dnsmessage.TypeAAAA, make([]byte, 15)},
		{"long AAAA", dnsmessage.TypeAAAA, make([]byte, 17)},
		{"CNAME short pointer", dnsmessage.TypeCNAME, []byte{0xc0}},
		{"CNAME trailing", dnsmessage.TypeCNAME, []byte{0xc0, 12, 0}},
		{"NS borrowed name", dnsmessage.TypeNS, []byte{3, 'n'}},
		{"SOA short integers", dnsmessage.TypeSOA, append([]byte{0xc0, 12, 0xc0, 12}, make([]byte, 19)...)},
		{"pointer into label", dnsmessage.TypeCNAME, []byte{0xc0, 13}},
		{"pointer outside", dnsmessage.TypeCNAME, []byte{0xff, 0xff}},
		{"reserved label", dnsmessage.TypeCNAME, []byte{0x40, 0}},
	} {
		t.Run(tt.label, func(t *testing.T) {
			if _, e := q.MatchResponse(peer, wire(tt.kind, tt.body)); e != ErrResponse {
				t.Fatal("accepted malformed RDATA")
			}
		})
	}
	b := wire(dnsmessage.TypeA, []byte{192, 0, 2, 1})
	b[len(q.packet)+1] = byte(len(q.packet))
	if _, e := q.MatchResponse(peer, b); e != ErrResponse {
		t.Fatal("accepted owner pointer loop")
	}
	b = wire(dnsmessage.TypeA, []byte{192, 0, 2, 1})
	b[len(q.packet)+5] = 3
	if _, e := q.MatchResponse(peer, b); e != ErrResponse {
		t.Fatal("accepted non-IN resource")
	}
	// A 512-byte response is accepted; the next byte exceeds the classic UDP cap.
	b = wire(dnsmessage.Type(99), make([]byte, 512-len(q.packet)-12))
	if len(b) != 512 {
		t.Fatal(len(b))
	}
	if got, e := q.MatchResponse(peer, b); e != nil || got.Answer != nq.DNSAnswerUnclassified {
		t.Fatalf("boundary: %+v %v", got, e)
	}
	// Case variation in the echoed question is legal.
	m := response(q)
	m.Questions[0].Name = name("TEST.EXAMPLE.")
	if _, e := q.MatchResponse(peer, pack(t, m)); e != nil {
		t.Fatal(e)
	}
}
