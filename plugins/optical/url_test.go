package cutting_garden_plugin_optical

import (
	"net/url"
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

func TestParseSource(t *testing.T) {
	cases := []struct {
		name       string
		arg        string
		wantDevice string
		wantMode   string
		wantErr    bool
		errSnips   []string
	}{
		{
			name:       "default image mode",
			arg:        "optical:/dev/sr0",
			wantDevice: "/dev/sr0",
			wantMode:   modeImage,
		},
		{
			name:       "explicit image mode",
			arg:        "optical:/dev/cdrom?mode=image",
			wantDevice: "/dev/cdrom",
			wantMode:   modeImage,
		},
		{
			name:       "audio mode",
			arg:        "optical:/dev/sr0?mode=audio",
			wantDevice: "/dev/sr0",
			wantMode:   modeAudio,
		},
		{
			name:       "triple slash host-empty form",
			arg:        "optical:///dev/sr0",
			wantDevice: "/dev/sr0",
			wantMode:   modeImage,
		},
		{
			name:     "host instead of path",
			arg:      "optical://dev/sr0",
			wantErr:  true,
			errSnips: []string{"unexpected host", "not `optical://"},
		},
		{
			name:     "relative device rejected",
			arg:      "optical:dev/sr0",
			wantErr:  true,
			errSnips: []string{"must be absolute"},
		},
		{
			name:     "unknown mode rejected",
			arg:      "optical:/dev/sr0?mode=bluray",
			wantErr:  true,
			errSnips: []string{"unknown mode", "mode=image", "mode=audio"},
		},
		{
			name:     "wrong scheme rejected",
			arg:      "file:/dev/sr0",
			wantErr:  true,
			errSnips: []string{"unsupported scheme"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.arg)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", tc.arg, err)
			}
			got, err := parseSource(u)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseSource(%q) = %+v, want error", tc.arg, got)
				}
				for _, snip := range tc.errSnips {
					if !strings.Contains(err.Error(), snip) {
						t.Errorf("error %q missing snippet %q", err.Error(), snip)
					}
				}
				// Every rejection here is a malformed CALLER argument and
				// must classify as a bad request, so the wire reports
				// -32602 rather than "plugin failed" (cutting-garden#187).
				if !errors.Is400BadRequest(err) {
					t.Errorf("parseSource(%q) error must classify as a"+
						" caller fault: %v", tc.arg, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSource(%q) error: %v", tc.arg, err)
			}
			if got.device != tc.wantDevice {
				t.Errorf("device = %q, want %q", got.device, tc.wantDevice)
			}
			if got.mode != tc.wantMode {
				t.Errorf("mode = %q, want %q", got.mode, tc.wantMode)
			}
		})
	}
}

func TestToolInvocation(t *testing.T) {
	t.Run("image uses ddrescue with image+map output", func(t *testing.T) {
		bin, args := toolInvocation(opticalSource{device: "/dev/sr0", mode: modeImage})
		if bin != ddrescueBin {
			t.Errorf("bin = %q, want %q", bin, ddrescueBin)
		}
		joined := strings.Join(args, " ")
		for _, want := range []string{"/dev/sr0", imageFilename, mapFilename} {
			if !strings.Contains(joined, want) {
				t.Errorf("args %v missing %q", args, want)
			}
		}
	})

	t.Run("audio uses cdparanoia batch mode", func(t *testing.T) {
		bin, args := toolInvocation(opticalSource{device: "/dev/cdrom", mode: modeAudio})
		if bin != cdparanoiaBin {
			t.Errorf("bin = %q, want %q", bin, cdparanoiaBin)
		}
		joined := strings.Join(args, " ")
		for _, want := range []string{"-d", "/dev/cdrom", "-B"} {
			if !strings.Contains(joined, want) {
				t.Errorf("args %v missing %q", args, want)
			}
		}
	})
}

func TestValidateSource(t *testing.T) {
	p := Plugin{}
	good := mustParseURL(t, "optical:/dev/sr0?mode=audio")
	bad := mustParseURL(t, "optical://dev/sr0")

	if err := p.ValidateSource(good, "optical:/dev/sr0?mode=audio"); err != nil {
		t.Errorf("ValidateSource(good): unexpected error %v", err)
	}
	if err := p.ValidateSource(bad, "optical://dev/sr0"); err == nil {
		t.Error("ValidateSource(bad): want error, got nil")
	}
}

func mustParseURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", s, err)
	}
	return u
}
