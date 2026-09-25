package contract

// ModelRequest is the input to a single model call (the loop -> adapter direction).
//
// It is deliberately not StreamInput: one agent execution may contain many model calls, each with
// its own composed history and tool surface. StreamInput is what a client hands the agent;
// ModelRequest is what the agent hands a model.
type ModelRequest struct {
	Messages []Message
	Tools    []ToolInfo // tool surface advertised to the model; empty means no tools offered
}
