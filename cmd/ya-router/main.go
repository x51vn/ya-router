// Command ya-router is the compatibility binary.
package main

import (
	"os"

	yarouter "github.com/duvu/ya-router/src"
)

func main() {
	os.Exit(yarouter.Execute(os.Args))
}
