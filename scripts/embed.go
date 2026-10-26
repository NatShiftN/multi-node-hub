// Package scripts exposes bootstrap assets embedded in the control-plane binary.
package scripts

import _ "embed"

// InstallScript is served at /install.sh and executed by the one-time SSH
// bootstrap. It must never echo the NodeToken.
//
//go:embed install.sh
var InstallScript []byte
