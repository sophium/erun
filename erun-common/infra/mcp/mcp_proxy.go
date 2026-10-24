package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/sophium/erun/erun-common/base/seam"
)

// Stdio side of the MCP edge. Clients cannot refresh a configured header, so the relay
// mints the bearer per request instead of writing it into the client's config.

const jsonRPCInternalError = -32603

type MCPStdioProxyParams struct {
	Endpoint      string
	MintToken     seam.MCPTokenMinter
	ClientVersion string
	In            io.Reader
	Out           io.Writer
	// Diagnostics carries human-facing lines; Out must carry only JSON-RPC.
	Diagnostics io.Writer
	// DescribeError renders a relay failure for the client; nil uses the raw error.
	DescribeError func(error) string
	LocalTools    []MCPLocalTool
}

// RunMCPStdioProxy relays newline-delimited JSON-RPC between stdin and the edge until stdin closes.
// Serial relay keeps the edge's session bookkeeping in the client's order.
func RunMCPStdioProxy(ctx context.Context, params MCPStdioProxyParams) error {
	if params.In == nil || params.Out == nil {
		return fmt.Errorf("MCP stdio proxy requires an input and an output stream")
	}
	// A relayed client is real activity, never an idle probe. awaitStartup is set because its
	// initialize is not retried.
	session, err := newMCPSession(params.Endpoint, params.MintToken, params.ClientVersion, false, true)
	if err != nil {
		return err
	}
	proxy := &mcpStdioProxy{session: session, params: params}
	session.notice = proxy.diagnose
	reader := bufio.NewReader(params.In)
	for {
		message, err := readJSONRPCLine(reader)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read MCP client message: %w", err)
		}
		if len(message) == 0 {
			continue
		}
		if err := proxy.relay(ctx, message); err != nil {
			return err
		}
	}
}

type mcpStdioProxy struct {
	session *mcpSession
	params  MCPStdioProxyParams
}

// relay writes at most one line. Failures answer with the request's id (notifications only reach
// diagnostics), and the relay keeps serving through edge outages.
func (p *mcpStdioProxy) relay(ctx context.Context, message []byte) error {
	id := jsonRPCMessageID(message)
	if tool, arguments, ok := McpLocalToolFor(message, p.params.LocalTools); ok {
		return p.answerLocally(ctx, id, tool, arguments)
	}
	reply, err := p.session.postRaw(ctx, message)
	if err == nil && len(reply) > 0 && mcpIsToolsList(message) {
		reply = McpAppendLocalTools(reply, p.params.LocalTools)
	}
	switch {
	case err != nil:
		p.diagnose(err.Error())
		return p.writeError(id, p.describe(err))
	case len(reply) > 0:
		return p.writeReply(reply)
	case len(id) > 0:
		detail := fmt.Sprintf("the MCP edge at %s accepted the request but returned no reply", p.params.Endpoint)
		p.diagnose(detail)
		return p.writeError(id, detail)
	}
	return nil
}

func (p *mcpStdioProxy) answerLocally(ctx context.Context, id json.RawMessage, tool MCPLocalTool, arguments json.RawMessage) error {
	if len(id) == 0 {
		return nil
	}
	var text string
	var callErr error
	if tool.Call == nil {
		callErr = fmt.Errorf("the %s tool is not wired on this host", tool.Name)
	} else {
		text, callErr = tool.Call(ctx, arguments)
	}
	if callErr != nil {
		p.diagnose(callErr.Error())
	}
	reply, err := McpLocalToolReply(id, text, callErr)
	if err != nil {
		return p.writeError(id, err.Error())
	}
	return p.writeLine(reply)
}

// writeReply compacts so a pretty-printed reply stays one line.
func (p *mcpStdioProxy) writeReply(reply []byte) error {
	compact := new(bytes.Buffer)
	if err := json.Compact(compact, reply); err != nil {
		p.diagnose(fmt.Sprintf("the MCP edge at %s returned a reply that is not JSON: %v", p.params.Endpoint, err))
		return nil
	}
	return p.writeLine(compact.Bytes())
}

func (p *mcpStdioProxy) writeError(id json.RawMessage, message string) error {
	if len(id) == 0 {
		return nil
	}
	encoded, err := json.Marshal(mcpJSONRPCErrorReply{
		JSONRPC: "2.0",
		ID:      id,
		Error:   mcpJSONRPCError{Code: jsonRPCInternalError, Message: message},
	})
	if err != nil {
		return err
	}
	return p.writeLine(encoded)
}

func (p *mcpStdioProxy) writeLine(payload []byte) error {
	if _, err := p.params.Out.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("write MCP reply: %w", err)
	}
	return nil
}

func (p *mcpStdioProxy) diagnose(message string) {
	if p.params.Diagnostics == nil {
		return
	}
	_, _ = fmt.Fprintln(p.params.Diagnostics, "mcp proxy: "+message)
}

func (p *mcpStdioProxy) describe(err error) string {
	if p.params.DescribeError == nil {
		return err.Error()
	}
	return p.params.DescribeError(err)
}

type mcpJSONRPCErrorReply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   mcpJSONRPCError `json:"error"`
}

// readJSONRPCLine tolerates a final line without a trailing newline.
func readJSONRPCLine(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadBytes('\n')
	trimmed := bytes.TrimSpace(line)
	if err != nil {
		if errors.Is(err, io.EOF) && len(trimmed) > 0 {
			return trimmed, nil
		}
		return nil, err
	}
	return trimmed, nil
}

// jsonRPCMessageID returns nil for a notification, including a literal null id.
func jsonRPCMessageID(message []byte) json.RawMessage {
	var envelope struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(message, &envelope); err != nil {
		return nil
	}
	if len(bytes.TrimSpace(envelope.ID)) == 0 || bytes.Equal(bytes.TrimSpace(envelope.ID), []byte("null")) {
		return nil
	}
	return envelope.ID
}
