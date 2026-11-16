package admin

import (
	"fmt"
	"strings"
	"testing"
)

func TestAuditF10UUIDQueryBoundaries(t *testing.T) {
	ids := make([]string, 201)
	for i := range ids {
		ids[i] = fmt.Sprintf("id-%d", i)
	}
	for _, tc := range []struct {
		name, input string
		want        int
		invalid     bool
	}{{"empty", "", 0, false}, {"dedup-trim", " a, b,a, ,b ", 2, false}, {"blank-list", " , ", 0, true}, {"max-count", strings.Join(ids[:200], ","), 200, false}, {"over-count", strings.Join(ids, ","), 0, true}, {"over-item", strings.Repeat("a", 129), 0, true}, {"over-bytes", strings.Repeat("a", 8193), 0, true}} {
		t.Run(tc.name, func(t *testing.T) {
			q := PeerQuery{Uuids: tc.input}
			got, err := q.UUIDList()
			if (err != nil) != tc.invalid {
				t.Fatalf("error=%v", err)
			}
			if err == nil && len(got) != tc.want {
				t.Fatalf("length=%d want=%d", len(got), tc.want)
			}
		})
	}
}
