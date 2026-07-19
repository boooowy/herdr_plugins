package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
)

// herdrClient talks to the running herdr instance over its unix domain socket.
// The protocol is newline-delimited JSON: one request object per line, one
// response object per line. Each call opens a short-lived connection, writes a
// single request, and reads a single response. herdr injects HERDR_SOCKET_PATH
// into every plugin command, so this works whenever herdr runs us.
type herdrClient struct {
	socketPath string
}

// newHerdrClient builds a client from the HERDR_SOCKET_PATH environment
// variable. It returns an error when the process is not running inside herdr.
func newHerdrClient() (*herdrClient, error) {
	path := os.Getenv("HERDR_SOCKET_PATH")
	if path == "" {
		return nil, errors.New("HERDR_SOCKET_PATH is not set; are you running inside herdr?")
	}
	return &herdrClient{socketPath: path}, nil
}

// request is one JSON-RPC-style message sent to herdr.
type request struct {
	ID     string         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

// herdrError carries the code and human message herdr returns on failure.
type herdrError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// response is one JSON line returned by herdr. Exactly one of Result or Error
// is populated.
type response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *herdrError     `json:"error"`
}

// call sends a single request over a fresh connection and decodes the result
// into out (which may be nil when the caller does not care about the payload).
func (c *herdrClient) call(method string, params map[string]any, out any) error {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("connect herdr socket: %w", err)
	}
	defer conn.Close()

	// json.Encoder.Encode appends a trailing newline, which is exactly the
	// framing herdr expects for each request.
	if err := json.NewEncoder(conn).Encode(request{ID: "hint-copy", Method: method, Params: params}); err != nil {
		return fmt.Errorf("write request: %w", err)
	}

	var resp response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("herdr error %s: %s", resp.Error.Code, resp.Error.Message)
	}
	if out != nil {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("decode result: %w", err)
		}
	}
	return nil
}

// paneRead returns the text currently shown in a pane. source selects which
// slice of the terminal to read ("visible" for the on-screen rows, "recent"
// for scrollback). ANSI is stripped by herdr (strip_ansi defaults to true).
func (c *herdrClient) paneRead(paneID, source string) (string, error) {
	var out struct {
		Read struct {
			Text string `json:"text"`
		} `json:"read"`
	}
	err := c.call("pane.read", map[string]any{
		"pane_id": paneID,
		"source":  source,
	}, &out)
	if err != nil {
		return "", err
	}
	return out.Read.Text, nil
}

// focusedPaneID returns the id of the currently focused pane. Actions run
// server-side and may not set HERDR_PANE_ID, so this is the fallback.
func (c *herdrClient) focusedPaneID() (string, error) {
	var out struct {
		Panes []struct {
			PaneID  string `json:"pane_id"`
			Focused bool   `json:"focused"`
		} `json:"panes"`
	}
	if err := c.call("pane.list", map[string]any{}, &out); err != nil {
		return "", err
	}
	for _, p := range out.Panes {
		if p.Focused {
			return p.PaneID, nil
		}
	}
	return "", errors.New("no focused pane")
}

// layoutRect is a pane/tab rectangle in absolute terminal cells (herdr's
// sidebar and tab bar offset x/y; rects tile the tab area exactly, with each
// pane drawing its 1-cell border ring inside its own rect).
type layoutRect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// paneLayoutResult is the tab geometry snapshot for a pane's tab.
type paneLayoutResult struct {
	Zoomed        bool       `json:"zoomed"`
	Area          layoutRect `json:"area"`
	FocusedPaneID string     `json:"focused_pane_id"`
	Panes         []struct {
		PaneID  string     `json:"pane_id"`
		Focused bool       `json:"focused"`
		Rect    layoutRect `json:"rect"`
	} `json:"panes"`
}

// paneLayout returns the layout of the tab containing paneID.
func (c *herdrClient) paneLayout(paneID string) (*paneLayoutResult, error) {
	var out struct {
		Layout paneLayoutResult `json:"layout"`
	}
	err := c.call("pane.layout", map[string]any{"pane_id": paneID}, &out)
	if err != nil {
		return nil, err
	}
	return &out.Layout, nil
}

// paneReadFull is pane.read with every knob: source, an optional trailing-
// lines cap (reported back via truncated), and raw ANSI capture (colors kept)
// when ansi is true.
func (c *herdrClient) paneReadFull(paneID, source string, lines int, ansi bool) (string, bool, error) {
	var out struct {
		Read struct {
			Text      string `json:"text"`
			Truncated bool   `json:"truncated"`
		} `json:"read"`
	}
	params := map[string]any{
		"pane_id": paneID,
		"source":  source,
	}
	if lines > 0 {
		params["lines"] = lines
	}
	if ansi {
		params["format"] = "ansi"
		params["strip_ansi"] = false
	}
	err := c.call("pane.read", params, &out)
	if err != nil {
		return "", false, err
	}
	return out.Read.Text, out.Read.Truncated, nil
}

// paneTitles maps pane ids to their display titles (user label first, then
// the stripped terminal title), for reproducing herdr's border captions.
func (c *herdrClient) paneTitles() map[string]string {
	var out struct {
		Panes []struct {
			PaneID                string `json:"pane_id"`
			Label                 string `json:"label"`
			TerminalTitleStripped string `json:"terminal_title_stripped"`
		} `json:"panes"`
	}
	if err := c.call("pane.list", map[string]any{}, &out); err != nil {
		return nil
	}
	m := make(map[string]string, len(out.Panes))
	for _, p := range out.Panes {
		title := p.Label
		if title == "" {
			title = p.TerminalTitleStripped
		}
		m[p.PaneID] = title
	}
	return m
}

// notify shows a toast notification inside herdr. sound is one of
// "none", "done", "request".
func (c *herdrClient) notify(title, body, sound string) error {
	return c.call("notification.show", map[string]any{
		"title": title,
		"body":  body,
		"sound": sound,
	}, nil)
}

// pluginPaneOpen asks herdr to open one of this plugin's [[panes]] entrypoints.
// env is forwarded into the pane process's environment; target_pane_id anchors
// the overlay over the pane the user launched from.
func (c *herdrClient) pluginPaneOpen(pluginID, entrypoint, placement, targetPaneID string, focus bool, env map[string]string) error {
	params := map[string]any{
		"plugin_id":  pluginID,
		"entrypoint": entrypoint,
		"placement":  placement,
		"focus":      focus,
	}
	if targetPaneID != "" {
		params["target_pane_id"] = targetPaneID
	}
	if len(env) > 0 {
		params["env"] = env
	}
	return c.call("plugin.pane.open", params, nil)
}
