// Command ya is the operating-system client boundary.
package main

import (
	"os"

	yarouter "github.com/duvu/ya-router/src"
)

func main() {
	os.Exit(yarouter.ExecuteClient(os.Args))
}
