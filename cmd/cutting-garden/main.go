package main

import (
	"os"

	"github.com/amarbel-llc/cutting-garden/internal/capture"
	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/restore"
	// Blank-import the file plugin so its init() registers under
	// the "" and "file" capture/restore/diff schemes before any
	// command dispatches.
	_ "github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugin_file"
	// Blank-import markl's purpose registrations so the blech32 decoder
	// can resolve `madder-private_key-v1`, `repo-pub-v1`, etc. ids that
	// appear in store configs. Without this, blob_store_env discovery
	// fails with "no purpose registered for id ..." on the first store
	// whose config carries an encryption key.
	_ "github.com/amarbel-llc/madder/go/pkgs/markl_registrations"
)

func main() {
	utility := command.MakeUtility("cutting-garden", nil)
	command.RegisterComplete(&utility)
	utility.AddCmd("capture", capture.New())
	utility.AddCmd("restore", restore.New())
	os.Exit(utility.Run(os.Args))
}
