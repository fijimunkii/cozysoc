package resolverwire

import (
	"strings"

	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"golang.org/x/net/dns/dnsmessage"
)

func classify(q Query, answers, authorities []dnsmessage.Resource) networkquality.DNSAnswerKind {
	current := strings.ToLower(q.name.String())
	visited := make(map[string]bool)
	aliases := 0
	for {
		if visited[current] {
			return networkquality.DNSAnswerUnclassified
		}
		visited[current] = true
		target := ""
		found := false
		for _, rr := range answers {
			if strings.ToLower(rr.Header.Name.String()) != current {
				continue
			}
			if rr.Header.Type == q.kind {
				found = true
			}
			if alias, ok := rr.Body.(*dnsmessage.CNAMEResource); ok {
				next := strings.ToLower(alias.CNAME.String())
				if target != "" && target != next {
					return networkquality.DNSAnswerUnclassified
				}
				target = next
			}
		}
		if found {
			if target != "" {
				return networkquality.DNSAnswerUnclassified
			}
			return networkquality.DNSAnswerPresent
		}
		if target == "" {
			break
		}
		aliases++
		if aliases > MaxAliases {
			return networkquality.DNSAnswerUnclassified
		}
		current = target
	}
	// Unrelated or wrong-type answer records are not proof of either a successful
	// answer or NODATA. Every answer must belong to the already traversed CNAME chain.
	for _, rr := range answers {
		if rr.Header.Type != dnsmessage.TypeCNAME || !visited[strings.ToLower(rr.Header.Name.String())] {
			return networkquality.DNSAnswerUnclassified
		}
	}
	soa, ns, anySOA, anyNS := false, false, false, false
	for _, rr := range authorities {
		relevant := inZone(current, strings.ToLower(rr.Header.Name.String()))
		switch rr.Header.Type {
		case dnsmessage.TypeSOA:
			anySOA = true
			soa = soa || relevant
		case dnsmessage.TypeNS:
			anyNS = true
			ns = ns || relevant
		}
	}
	if soa {
		return networkquality.DNSAnswerNoData
	}
	if anySOA {
		return networkquality.DNSAnswerUnclassified
	}
	if ns {
		return networkquality.DNSAnswerReferral
	}
	if anyNS || aliases > 0 {
		return networkquality.DNSAnswerUnclassified
	}
	// RFC 2308: NOERROR with no relevant answer and no authority NS can be NODATA.
	// A CNAME-only response without negative evidence remains unknown above.
	return networkquality.DNSAnswerNoData
}

func inZone(name, zone string) bool {
	return zone == "." || name == zone || strings.HasSuffix(name, "."+zone)
}
