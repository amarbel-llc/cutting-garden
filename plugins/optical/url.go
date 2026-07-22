package cutting_garden_plugin_optical

import (
	"net/url"
	"strings"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// opticalScheme is the single URI scheme this plugin claims.
const opticalScheme = "optical"

// Capture modes selected via the `?mode=` query parameter.
const (
	// modeImage rips a full disc image with GNU ddrescue (default).
	modeImage = "image"
	// modeAudio rips audio-CD tracks to WAV with cdparanoia.
	modeAudio = "audio"
)

// External tool binary names, resolved via exec.LookPath at capture
// time. The Nix flake wraps cutting-garden so both are on PATH.
const (
	ddrescueBin   = "ddrescue"
	cdparanoiaBin = "cdparanoia"
)

// Artifact filenames written by the image-mode rip into the capture
// tempdir (cmd.Dir): the disc image and its ddrescue rescue map. The
// map records which sectors were recovered vs. still bad — worth
// capturing as provenance alongside the image. Audio mode lets
// cdparanoia name its own `trackNN.cdda.wav` files.
const (
	imageFilename = "disc.iso"
	mapFilename   = "disc.iso.map"
)

// opticalSource is the parsed form of an `optical:` argument: the
// device path to read and the rip mode.
type opticalSource struct {
	device string
	mode   string
}

// parseSource decodes an optical:// argument into the device path and
// rip mode. Accepted forms:
//
//   - optical:/dev/sr0              device = /dev/sr0, mode = image
//   - optical:/dev/cdrom?mode=audio device = /dev/cdrom, mode = audio
//   - optical:///dev/sr0            host-empty triple-slash form
//
// The device path MUST be absolute. A bare host (optical://dev/sr0) is
// rejected: the device is a path, not a host. Returns an error suitable
// for ValidateSource refusal.
func parseSource(u *url.URL) (opticalSource, error) {
	// Malformed source URIs are CALLER mistakes: bad requests
	// (errors.Is400BadRequest true, -32602 over the RFC 0013 wire), not
	// plugin failures that invite a futile retry (cutting-garden#187).
	if u.Scheme != opticalScheme {
		return opticalSource{}, errors.BadRequestf(
			"optical plugin: unsupported scheme %q in %q",
			u.Scheme, u.String(),
		)
	}

	if u.Host != "" {
		return opticalSource{}, errors.BadRequestf(
			"optical plugin: unexpected host %q in %q\n"+
				"hint: the device is a path — write `optical:/dev/sr0`, not `optical://dev/sr0`",
			u.Host, u.String(),
		)
	}

	// url.Parse routes the post-colon segment to Path when it begins
	// with `/` (optical:/dev/sr0) and to Opaque otherwise
	// (optical:dev/sr0). Prefer Path; fall back to Opaque so the
	// absolute-path check below produces the actionable error.
	device := u.Path
	if device == "" {
		device = u.Opaque
	}
	if device == "" {
		return opticalSource{}, errors.BadRequestf(
			"optical plugin: empty device path in %q\n"+
				"hint: pass `optical:/dev/sr0` (your optical drive's device node)",
			u.String(),
		)
	}
	if !strings.HasPrefix(device, "/") {
		return opticalSource{}, errors.BadRequestf(
			"optical plugin: device path %q must be absolute in %q\n"+
				"hint: pass `optical:/dev/sr0`",
			device, u.String(),
		)
	}

	mode := u.Query().Get("mode")
	if mode == "" {
		mode = modeImage
	}
	switch mode {
	case modeImage, modeAudio:
	default:
		return opticalSource{}, errors.BadRequestf(
			"optical plugin: unknown mode %q in %q\n"+
				"hint: use `?mode=image` (ddrescue, default) or `?mode=audio` (cdparanoia)",
			mode, u.String(),
		)
	}

	return opticalSource{device: device, mode: mode}, nil
}

// toolInvocation maps a parsed source to the external binary and its
// argument vector. The command runs with cmd.Dir set to the capture
// tempdir, so all output filenames are relative and land there.
//
//   - image: ddrescue -b 2048 -r 3 <device> disc.iso disc.iso.map
//     2048-byte blocks match the optical data-sector size; three retry
//     passes recover marginal sectors before giving up.
//   - audio: cdparanoia -d <device> -B
//     batch mode (-B) writes one trackNN.cdda.wav per track.
func toolInvocation(src opticalSource) (bin string, args []string) {
	switch src.mode {
	case modeAudio:
		return cdparanoiaBin, []string{"-d", src.device, "-B"}
	default: // modeImage
		return ddrescueBin, []string{
			"-b", "2048",
			"-r", "3",
			src.device,
			imageFilename,
			mapFilename,
		}
	}
}
