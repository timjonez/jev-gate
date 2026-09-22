package main

import (
	"os"

	"github.com/timjonez/gate/internal/gatecli"
)

func main() {
	os.Exit(gatecli.NewApp().Execute(os.Args[1:]))
}
