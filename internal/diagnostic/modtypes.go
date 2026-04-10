package diagnostic

// ── Module-specific types ───────────────────────────────────────────────────
//
// These types are defined in the diagnostic package so that both sub-package
// modules (which create them) and fixgen.go (which reads them) can access them
// without circular imports. Each module stores its stats via SetModuleData()
// and readers use GetModuleData() + type assertion.

// RTKAnalysis holds pre-computed RTK-specific metrics for detectors.
type RTKAnalysis struct {
	TotalRTKCmds    int          // total commands run via rtk
	CurlViaRTK      int          // curl commands run via rtk
	CurlDirect      int          // curl commands run directly (no rtk)
	RedactedOutputs int          // tool outputs containing RTK redaction markers
	RetryBursts     []RetryBurst // sequences of 3+ identical rtk commands
}

// RetryBurst describes a sequence of identical RTK commands.
type RetryBurst struct {
	Command     string `json:"command"`
	Count       int    `json:"count"`
	StartMsgIdx int    `json:"start_msg_idx"`
	EndMsgIdx   int    `json:"end_msg_idx"`
}

// GetRTKStats retrieves RTK analysis data from a report.
// Returns nil if the RTK module hasn't run yet.
func GetRTKStats(r *InspectReport) *RTKAnalysis {
	d := r.GetModuleData("rtk")
	if d == nil {
		return nil
	}
	return d.(*RTKAnalysis)
}

// APIAnalysis holds pre-computed API-specific metrics for detectors.
type APIAnalysis struct {
	EndpointCalls []EndpointCall `json:"endpoint_calls,omitempty"`
	CommandBursts []CommandBurst `json:"command_bursts,omitempty"`
}

// EndpointCall tracks how many times a specific URL was targeted.
type EndpointCall struct {
	URL   string `json:"url"`
	Count int    `json:"count"`
}

// CommandBurst is a cluster of identical commands within a tight message window.
type CommandBurst struct {
	Command     string `json:"command"`
	Count       int    `json:"count"`
	StartMsgIdx int    `json:"start_msg_idx"`
	EndMsgIdx   int    `json:"end_msg_idx"`
}

// GetAPIStats retrieves API analysis data from a report.
// Returns nil if the API module hasn't run yet.
func GetAPIStats(r *InspectReport) *APIAnalysis {
	d := r.GetModuleData("api")
	if d == nil {
		return nil
	}
	return d.(*APIAnalysis)
}
