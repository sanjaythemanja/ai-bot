package assistant

// Step is one unit of visible progress in an agent run: a model call or a
// tool call. The same Step is reported again as its status changes.
type Step struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`           // StepModel or StepTool
	Name   string `json:"name,omitempty"` // tool name
	Hint   string `json:"hint,omitempty"` // e.g. the meeting code or query
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Step kinds and statuses.
const (
	StepModel = "model"
	StepTool  = "tool"

	StepRunning     = "running"
	StepSummarizing = "summarizing"
	StepDone        = "done"
	StepError       = "error"
)

func (s Step) end(err error) Step {
	s.Status = StepDone
	if err != nil {
		s.Status, s.Error = StepError, err.Error()
	}
	return s
}
