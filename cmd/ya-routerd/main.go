// Command ya-routerd runs the long-lived service process.
package main

import (
	"os"

	yarouter "github.com/duvu/ya-router/src"
)

func main() {
	os.Exit(yarouter.ExecuteDaemon(os.Args))
}
