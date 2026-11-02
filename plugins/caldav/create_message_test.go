package caldav

import (
	"context"
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// A rejected create or put body names what is accepted, the likely cause when
// it is known, and where the example lives. (A JSON create body with no uid is
// no longer rejected: its uid is minted, see create_uid_test.go.)
func TestCreateChild_RejectionNamesTheAcceptedFormats(t *testing.T) {
	cases := []struct {
		name, body string
		want       []string
	}{
		{
			"raw iCalendar without a uid",
			"BEGIN:VCALENDAR\nBEGIN:VTODO\nSUMMARY:x\nEND:VTODO\nEND:VCALENDAR\n",
			[]string{"VTODO missing UID", "raw iCalendar", "describe_node_types"},
		},
		{
			"free text",
			"buy milk",
			[]string{"raw iCalendar", `\,`, "{component", "describe_node_types"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, home := startFakeEmpty(t)
			_, err := Plugin{}.CreateChild(
				context.Background(), mustParseURL(t, objectArg(home, "/dav/cal/")),
				strings.NewReader(tc.body), typeVTODO)
			if err == nil || !errors.Is400BadRequest(err) {
				t.Fatalf("create = %v; want a bad request", err)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
		})
	}
}
