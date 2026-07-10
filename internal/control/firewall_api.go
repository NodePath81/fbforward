package control

// Firewall RPCs are intentionally not public yet. Keeping this registration
// point separate makes online-rule APIs additive without expanding the current
// control surface or changing the existing RPC contract.
func (c *ControlServer) registerFirewallRPCs() {
	// Reserved for the online firewall rule API.
}
