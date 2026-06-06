package cutting_garden_plugin_ytdlp

import "testing"

func TestParseProgressLine(t *testing.T) {
	cases := []struct {
		name   string
		line   string
		want   progressSample
		wantOK bool
	}{
		{
			name:   "well-formed total",
			line:   "CGP\t1024\t4096\t\tabc123",
			want:   progressSample{Downloaded: 1024, Total: 4096, ID: "abc123"},
			wantOK: true,
		},
		{
			name:   "total NA uses estimate",
			line:   "CGP\t1024\tNA\t8192\tabc123",
			want:   progressSample{Downloaded: 1024, Total: 8192, ID: "abc123"},
			wantOK: true,
		},
		{
			name:   "total none uses estimate",
			line:   "CGP\t512\tnone\t2048\txyz",
			want:   progressSample{Downloaded: 512, Total: 2048, ID: "xyz"},
			wantOK: true,
		},
		{
			name:   "total empty uses estimate",
			line:   "CGP\t256\t\t1000\tvid",
			want:   progressSample{Downloaded: 256, Total: 1000, ID: "vid"},
			wantOK: true,
		},
		{
			name:   "both total and estimate NA",
			line:   "CGP\t128\tNA\tNA\tvid",
			want:   progressSample{Downloaded: 128, Total: 0, ID: "vid"},
			wantOK: true,
		},
		{
			name:   "downloaded NA treated as zero",
			line:   "CGP\tNA\t4096\t\tabc",
			want:   progressSample{Downloaded: 0, Total: 4096, ID: "abc"},
			wantOK: true,
		},
		{
			name:   "non-sentinel line",
			line:   "[download] Destination: foo.mp4",
			wantOK: false,
		},
		{
			name:   "empty line",
			line:   "",
			wantOK: false,
		},
		{
			name:   "sentinel prefix but truncated fields",
			line:   "CGP\t1024",
			wantOK: false,
		},
		{
			name:   "malformed numerics do not crash",
			line:   "CGP\tnotanint\talsobad\tweird\tabc",
			want:   progressSample{Downloaded: 0, Total: 0, ID: "abc"},
			wantOK: true,
		},
		{
			name:   "estimate malformed falls to zero",
			line:   "CGP\t100\tNA\tnotanint\tabc",
			want:   progressSample{Downloaded: 100, Total: 0, ID: "abc"},
			wantOK: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseProgressLine(tc.line)
			if ok != tc.wantOK {
				t.Fatalf("parseProgressLine(%q) ok = %v, want %v", tc.line, ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if got != tc.want {
				t.Errorf("parseProgressLine(%q) = %+v, want %+v", tc.line, got, tc.want)
			}
		})
	}
}
