// Command bomly-plugin-bun-lock-detector serves the Bun lock detector as a
// managed Bomly plugin over the HashiCorp go-plugin gRPC transport. The
// binary is launched and supervised by Bomly; it is not meant to be run by
// hand.
package main

import (
	sdk "github.com/bomly-dev/bomly-sdk"

	"github.com/bomly-dev/bomly-plugin-bun-lock-detector/plugin"
)

func main() { sdk.ServeModule(plugin.Module()) }
