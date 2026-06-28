// Command opendeezer-mcp is a Model Context Protocol (MCP) server that lets an
// AI agent control an OpenDeezer client's playback. It speaks JSON-RPC 2.0 over
// stdio (newline-delimited) and drives the running client through its control API
// (see internal/control). Point it at the client with $OPENDEEZER_CONTROL_URL
// (default http://127.0.0.1:7654); authenticate with $OPENDEEZER_CONTROL_TOKEN
// or $OPENDEEZER_CONTROL_ACCOUNT.
//
// Tools: get_status, play_pause, next, prev, stop, restart, cycle_repeat,
// toggle_shuffle, set_volume, seek, search, list_playlists, play_track,
// play_playlist.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Cycl0o0/OpenDeezer/internal/control"
)

var version = "1.0.0"

const protocolVersion = "2024-11-05"

func main() {
	base := env("OPENDEEZER_CONTROL_URL", "http://127.0.0.1:7654")
	client := control.NewClient(base, os.Getenv("OPENDEEZER_CONTROL_TOKEN"), os.Getenv("OPENDEEZER_CONTROL_ACCOUNT"))
	s := &server{client: client, tools: buildTools(client)}
	s.serve(os.Stdin, os.Stdout)
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// ---- JSON-RPC ----

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type server struct {
	client *control.Client
	tools  []tool
	out    *bufio.Writer
}

func (s *server) serve(in io.Reader, out io.Writer) {
	s.out = bufio.NewWriter(out)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req rpcReq
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			logf("parse error: %v", err)
			continue
		}
		s.dispatch(req)
	}
}

func (s *server) dispatch(req rpcReq) {
	switch req.Method {
	case "initialize":
		s.reply(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "opendeezer-mcp", "version": version},
		})
	case "tools/list":
		s.reply(req.ID, map[string]any{"tools": toolSpecs(s.tools)})
	case "tools/call":
		s.handleCall(req)
	case "ping":
		s.reply(req.ID, map[string]any{})
	default:
		// Notifications (e.g. notifications/initialized) have no id and need no
		// response; unknown requests get a method-not-found error.
		if len(req.ID) > 0 {
			s.replyErr(req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

func (s *server) handleCall(req rpcReq) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.replyErr(req.ID, -32602, "invalid params: "+err.Error())
		return
	}
	for _, t := range s.tools {
		if t.name == p.Name {
			text, err := t.run(p.Arguments)
			if err != nil {
				s.reply(req.ID, toolResult("error: "+err.Error(), true))
				return
			}
			s.reply(req.ID, toolResult(text, false))
			return
		}
	}
	s.reply(req.ID, toolResult("unknown tool: "+p.Name, true))
}

func toolResult(text string, isErr bool) map[string]any {
	r := map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
	if isErr {
		r["isError"] = true
	}
	return r
}

func (s *server) reply(id json.RawMessage, result any) {
	if len(id) == 0 {
		return // notification: no response
	}
	s.write(rpcResp{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *server) replyErr(id json.RawMessage, code int, msg string) {
	s.write(rpcResp{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func (s *server) write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		logf("marshal error: %v", err)
		return
	}
	_, _ = s.out.Write(b)
	_ = s.out.WriteByte('\n')
	_ = s.out.Flush()
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "opendeezer-mcp: "+format+"\n", args...)
}
