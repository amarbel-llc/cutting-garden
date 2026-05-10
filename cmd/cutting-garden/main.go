package main

import (
	"os"

	"github.com/amarbel-llc/cutting-garden/internal/command"
)

func main() {
	utility := command.MakeUtility("cutting-garden", nil)
	command.RegisterComplete(&utility)
	utility.Run(os.Args)
}
