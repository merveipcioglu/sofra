package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/shared/constant"

	"sofra/internal/action"
	"sofra/internal/domain"
	"sofra/internal/knowledge"
	"sofra/internal/tools"
	"sofra/internal/ui"
)

const (
	model    = "claude-sonnet-5-5"
	maxSteps = 12
)

type Session struct {
	UserID   domain.UserID
	turn     int
	messages []anthropic.BetaMessageParam
}

func NewSession(userID domain.UserID) *Session { return &Session{UserID: userID} }

type Agent struct {
	client anthropic.Client
	exec   *tools.Executor
	tools  []anthropic.BetaToolUnionParam
	trace  *json.Encoder
}

func New(client anthropic.Client, exec *tools.Executor, trace io.Writer) *Agent {
	a := &Agent{client: client, exec: exec, tools: append(toolParams(), finalToolParam())}
	if trace != nil {
		a.trace = json.NewEncoder(trace)
		a.trace.SetEscapeHTML(false)
	}
	return a
}

func (a *Agent) record(s *Session, kind string, fields map[string]any) {
	if a.trace == nil {
		return
	}
	fields["turn"], fields["kind"] = s.turn, kind
	_ = a.trace.Encode(fields)
}

func toolParams() []anthropic.BetaToolUnionParam {
	defs := tools.Definitions()
	out := make([]anthropic.BetaToolUnionParam, 0, len(defs))
	for _, d := range defs {
		out = append(out, toolParam(d.Name, d.Description, d.Properties))
	}
	return out
}

func finalToolParam() anthropic.BetaToolUnionParam {
	return toolParam(finalTool, "Your reply to the user. Call it alone, last, once you have read the results you need.", finalProperties)
}

func toolParam(name, description string, properties map[string]any) anthropic.BetaToolUnionParam {
	return anthropic.BetaToolUnionParam{OfTool: &anthropic.BetaToolParam{
		Name:        name,
		Description: anthropic.String(description),
		Strict:      anthropic.Bool(true),
		InputSchema: anthropic.BetaToolInputSchemaParam{
			Properties:  properties,
			Required:    slices.Sorted(maps.Keys(properties)),
			ExtraFields: map[string]any{"additionalProperties": false},
		},
	}}
}

func (s *Session) addUser(blocks ...anthropic.BetaContentBlockParamUnion) {
	if n := len(s.messages); n > 0 && s.messages[n-1].OfUser != nil {
		s.messages[n-1].OfUser.Content = append(s.messages[n-1].OfUser.Content, blocks...)
		return
	}
	s.messages = append(s.messages, anthropic.NewBetaUserMessage(blocks...))
}

type turnState struct {
	called   []string
	calls    []call
	problems []string
	reads    map[string]string
}

type call struct {
	id  string
	res tools.Result
}

func (a *Agent) Handle(ctx context.Context, s *Session, userText string) (ui.Response, error) {
	if token, ok := typedToken(userText); ok {
		return a.confirm(s, token, typedNote, "confirm_typed_token")
	}
	s.turn++
	s.addUser(anthropic.NewBetaTextBlock(userText))
	st := &turnState{reads: map[string]string{}}
	a.record(s, "user", map[string]any{"user_id": s.UserID, "text": userText})

	for step := 0; step < maxSteps; step++ {
		resp, err := a.client.Beta.Messages.New(ctx, a.params(s))
		if err != nil {
			a.record(s, "error", map[string]any{"step": step, "error": err.Error()})
			return failed(s, st, fmt.Errorf("claude: %w", err))
		}
		a.record(s, "response", map[string]any{"step": step, "response": json.RawMessage(resp.RawJSON())})
		s.messages = append(s.messages, resp.ToParam())

		switch resp.StopReason {
		case anthropic.BetaStopReasonToolUse:
			if reply, id, ok := finalCall(resp); ok {
				s.addUser(anthropic.NewBetaToolResultBlock(id, `{"delivered":true}`, false))
				return compose(s, st, reply), nil
			}
			s.addUser(a.runTools(s, st, resp)...)
		case anthropic.BetaStopReasonEndTurn:
			text := plainText(resp)
			if text == "" {
				return failed(s, st, fmt.Errorf("the model ended the turn without a reply"))
			}
			st.problems = append(st.problems, "the model replied in plain text, not with final_reply")
			return compose(s, st, finalReply{Markdown: text, Decision: string(ui.Answered)}), nil
		case anthropic.BetaStopReasonRefusal:
			s.closeToolUses(resp)
			return systemReply(s, st, "refused", "This request can't be handled.", "the model declined the request"), nil
		default:
			s.closeToolUses(resp)
			return failed(s, st, fmt.Errorf("claude stopped with %q", resp.StopReason))
		}
	}
	return systemReply(s, st, "step_limit", "This took too many steps; nothing more was done.", "tool step limit reached"), nil
}

const (
	screenNote  = "[screen]"
	pressedNote = screenNote + " The user acted on the confirmation screen, without you. Result: "
	typedNote   = screenNote + " The user typed a confirm_token in the chat; the system checked it without you. Result: "
)

var typedTokenPattern = regexp.MustCompile(`(?i)confirm_token\s*[=:]\s*["'` + "`" + `]?([^\s"'` + "`" + `]+)`)

func typedToken(text string) (string, bool) {
	m := typedTokenPattern.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	return strings.TrimRight(m[1], ".,;:!?)"), true
}

func (a *Agent) ConfirmOnScreen(s *Session, token string) (ui.Response, error) {
	return a.confirm(s, token, pressedNote, "confirm_on_screen")
}

func (a *Agent) confirm(s *Session, token, note, intent string) (ui.Response, error) {
	s.turn++
	st := &turnState{reads: map[string]string{}}
	res, err := a.exec.ConfirmOnScreen(s.UserID, s.turn, token)
	if err != nil {
		a.record(s, "error", map[string]any{"screen": true, "error": err.Error()})
		return systemReply(s, st, "confirmation_failed", "The confirmation could not be processed. Please try again.", err.Error()), err
	}
	a.record(s, "screen", map[string]any{"content": json.RawMessage(res.Content)})
	s.addUser(anthropic.NewBetaTextBlock(note + res.Content))

	base := ui.Audit{UserID: string(s.UserID), Intent: intent}
	if act := res.Outcome.Action; act != "" {
		base.ToolsCalled = []string{string(act)}
	}
	blocks, audit := fromOutcome(res, base)
	return ui.Response{Version: "1", Blocks: blocks, Audit: audit}, nil
}

func (s *Session) closeToolUses(resp *anthropic.BetaMessage) {
	var results []anthropic.BetaContentBlockParamUnion
	for _, block := range resp.Content {
		if use, ok := block.AsAny().(anthropic.BetaToolUseBlock); ok {
			results = append(results, anthropic.NewBetaToolResultBlock(use.ID, `{"error":"not run; nothing was executed"}`, true))
		}
	}
	if len(results) > 0 {
		s.addUser(results...)
	}
}

func failed(s *Session, st *turnState, err error) (ui.Response, error) {
	return systemReply(s, st, "assistant_unavailable",
		"The assistant could not finish this answer. Please try again.", err.Error()), err
}

func (a *Agent) params(s *Session) anthropic.BetaMessageNewParams {
	return anthropic.BetaMessageNewParams{
		Model:     model,
		MaxTokens: 16000,
		System: []anthropic.BetaTextBlockParam{{
			Text: systemPrompt, CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
		}},
		Tools:        a.tools,
		Messages:     s.messages,
		CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
		OutputConfig: anthropic.BetaOutputConfigParam{
			Effort: anthropic.BetaOutputConfigEffortMedium,
		},
		Thinking: anthropic.BetaThinkingConfigParamUnion{OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{
			Display: anthropic.BetaThinkingConfigAdaptiveDisplaySummarized,
		}},
		Fallbacks: anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()},
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
	}
}

func (a *Agent) runTools(s *Session, st *turnState, resp *anthropic.BetaMessage) []anthropic.BetaContentBlockParamUnion {
	var out []anthropic.BetaContentBlockParamUnion
	for _, block := range resp.Content {
		use, ok := block.AsAny().(anthropic.BetaToolUseBlock)
		if !ok {
			continue
		}
		if use.Name == finalTool {
			out = append(out, anthropic.NewBetaToolResultBlock(use.ID,
				`{"error":"call final_reply alone, after reading the other results"}`, true))
			continue
		}
		st.called = append(st.called, use.Name)
		input := use.JSON.Input.Raw()
		key := use.Name + " " + canonicalJSON(input)
		var res tools.Result
		if earlier, ok := st.reads[key]; ok {
			res = tools.Result{Content: repeatedCall(earlier)}
		} else if tools.Sensitive(use.Name) && st.hasOutcome() {
			st.problems = append(st.problems, "a second action in one turn was refused: "+use.Name)
			res = tools.Result{Content: `{"error":"not run: one action per turn. Report the outcome above, then ask the user about this action; it needs its own confirmation."}`, IsError: true}
		} else {
			var err error
			res, err = a.exec.Run(s.UserID, s.turn, use.Name, []byte(input))
			if err != nil {
				st.problems = append(st.problems, fmt.Sprintf("tool error: %s: %v", use.Name, err))
				res = tools.Result{Content: `{"error":"the tool failed; nothing was executed"}`, IsError: true}
			}
			switch {
			case tools.ReadOnly(use.Name):
				st.reads[key] = use.ID
			default:
				clear(st.reads)
			}
		}
		a.record(s, "tool_result", map[string]any{
			"tool_use_id": use.ID, "name": use.Name, "input": json.RawMessage(use.JSON.Input.Raw()),
			"content": json.RawMessage(res.Content), "is_error": res.IsError,
		})
		st.calls = append(st.calls, call{id: use.ID, res: res})
		out = append(out, anthropic.NewBetaToolResultBlock(use.ID, res.Content, res.IsError))
	}
	return out
}

func canonicalJSON(raw string) string {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return string(b)
}

func repeatedCall(earlier string) string {
	return fmt.Sprintf(`{"note":"You already made this exact call in this turn (tool_use %s). Its result is unchanged and was not fetched again. If you have what you need, give the final reply now."}`, earlier)
}

func finalCall(resp *anthropic.BetaMessage) (finalReply, string, bool) {
	var uses []anthropic.BetaToolUseBlock
	for _, block := range resp.Content {
		if use, ok := block.AsAny().(anthropic.BetaToolUseBlock); ok {
			uses = append(uses, use)
		}
	}
	if len(uses) != 1 || uses[0].Name != finalTool {
		return finalReply{}, "", false
	}
	var r finalReply
	if err := json.Unmarshal([]byte(uses[0].JSON.Input.Raw()), &r); err != nil {
		return finalReply{}, "", false
	}
	return r, uses[0].ID, true
}

func plainText(resp *anthropic.BetaMessage) string {
	var parts []string
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func compose(s *Session, st *turnState, r finalReply) ui.Response {
	audit := ui.Audit{UserID: string(s.UserID), Intent: r.Intent, ToolsCalled: st.called, Reason: st.problemSummary()}
	var blocks []ui.Block
	if r.Markdown != "" {
		blocks = append(blocks, ui.NewText(r.Markdown))
	}

	if res, ok := st.outcome(); ok {
		var outcomeBlocks []ui.Block
		outcomeBlocks, audit = fromOutcome(res, audit)
		blocks = append(blocks, outcomeBlocks...)
	} else {
		var reason string
		audit.Decision, audit.KBDocIDs, reason = st.decide(ui.Decision(r.Decision), r.CitedDocIDs)
		audit.Reason = strings.TrimPrefix(audit.Reason+"; "+reason, "; ")
		if audit.Decision != ui.Unknown {
			blocks = append(blocks, st.lookupCards()...)
		}
		blocks = append(blocks, st.citations(audit.KBDocIDs)...)
	}

	if len(r.SuggestedActions) > 0 {
		blocks = append(blocks, ui.NewSuggestedActions(r.SuggestedActions[:min(len(r.SuggestedActions), maxSuggestions)]...))
	}
	if len(blocks) == 0 {
		blocks = append(blocks, ui.NewText(r.Markdown))
	}
	return ui.Response{Version: "1", Blocks: blocks, Audit: audit}
}

func (st *turnState) lookupCards() []ui.Block {
	var blocks []ui.Block
	seen := map[string]bool{}
	for _, c := range st.calls {
		res := c.res
		if len(res.Blocks) == 0 || seen[res.Content] {
			continue
		}
		seen[res.Content] = true
		blocks = append(blocks, res.Blocks...)
	}
	return blocks
}

func (st *turnState) problemSummary() string {
	return strings.Join(st.problems, "; ")
}

func (st *turnState) outcome() (tools.Result, bool) {
	for _, c := range slices.Backward(st.calls) {
		if c.res.Outcome != nil {
			return c.res, true
		}
	}
	return tools.Result{}, false
}

func (st *turnState) hasOutcome() bool {
	return slices.ContainsFunc(st.calls, func(c call) bool {
		return c.res.Outcome != nil && c.res.Outcome.Status != action.Invalid
	})
}

func fromOutcome(res tools.Result, base ui.Audit) ([]ui.Block, ui.Audit) {
	blocks, audit := ui.FromOutcome(*res.Outcome, res.Params)
	audit.UserID, audit.Intent, audit.ToolsCalled, audit.Reason = base.UserID, base.Intent, base.ToolsCalled, base.Reason
	return blocks, audit
}

func (st *turnState) knowledge() []knowledge.Result {
	var all []knowledge.Result
	for _, c := range st.calls {
		all = append(all, c.res.Knowledge...)
	}
	return all
}

func (st *turnState) decide(proposed ui.Decision, cited []string) (ui.Decision, []string, string) {
	switch {
	case proposed == ui.Clarify || proposed == ui.Refused:
		return proposed, nil, ""
	case slices.Contains(st.called, tools.SearchKnowledge) || len(cited) > 0:
		ids, d := ui.GroundKnowledgeAnswer(proposed, cited, st.knowledge())
		if d == ui.Unknown {
			return d, nil, "no knowledge document returned this turn supports the answer"
		}
		return d, ids, ""
	case proposed == ui.Answered && len(st.called) == 0:
		return ui.Unknown, nil, "no tool result backs the answer"
	default:
		return proposed, nil, ""
	}
}

func (st *turnState) citations(ids []string) []ui.Block {
	var blocks []ui.Block
	for _, id := range ids {
		for _, r := range st.knowledge() {
			if r.ID == id {
				blocks = append(blocks, ui.NewCitation(r.Document))
				break
			}
		}
	}
	return blocks
}

func systemReply(s *Session, st *turnState, code, message, reason string) ui.Response {
	blocks := []ui.Block{ui.Error{Type: "error", Code: code, Message: message}}
	audit := ui.Audit{UserID: string(s.UserID), ToolsCalled: st.called, Decision: ui.Refused, Reason: reason}
	if res, ok := st.outcome(); ok {
		var outcomeBlocks []ui.Block
		outcomeBlocks, audit = fromOutcome(res, audit)
		blocks = append(outcomeBlocks, blocks...)
	}
	return ui.Response{Version: "1", Blocks: blocks, Audit: audit}
}
