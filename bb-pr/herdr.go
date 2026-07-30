// Snapshot of hint-copy/herdr.go as of 2029b96 (trimmed to the methods bb-pr
// needs; focusedPane gained cwd/foreground_cwd). Do not import hint-copy —
// it is being refactored independently.
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
	if err := json.NewEncoder(conn).Encode(request{ID: "bb-pr", Method: method, Params: params}); err != nil {
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

// paneInfo is the slice of pane.list bb-pr cares about: identity, focus, and
// the two working-directory fields (foreground_cwd is the cwd of the process
// holding the PTY — e.g. the shell the user is typing in — and is fresher
// than cwd).
type paneInfo struct {
	PaneID        string `json:"pane_id"`
	Focused       bool   `json:"focused"`
	Cwd           string `json:"cwd"`
	ForegroundCwd string `json:"foreground_cwd"`
}

// focusedPane returns the currently focused pane. Actions run server-side
// and may not set HERDR_PANE_ID, so this is the fallback for locating the
// pane the user launched from.
func (c *herdrClient) focusedPane() (*paneInfo, error) {
	var out struct {
		Panes []paneInfo `json:"panes"`
	}
	if err := c.call("pane.list", map[string]any{}, &out); err != nil {
		return nil, err
	}
	for i := range out.Panes {
		if out.Panes[i].Focused {
			return &out.Panes[i], nil
		}
	}
	return nil, errors.New("no focused pane")
}

// paneByID returns the pane with the given id from pane.list.
func (c *herdrClient) paneByID(id string) (*paneInfo, error) {
	var out struct {
		Panes []paneInfo `json:"panes"`
	}
	if err := c.call("pane.list", map[string]any{}, &out); err != nil {
		return nil, err
	}
	for i := range out.Panes {
		if out.Panes[i].PaneID == id {
			return &out.Panes[i], nil
		}
	}
	return nil, fmt.Errorf("pane %s not found", id)
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

// openedPane identifies the pane and tab a plugin.pane.open created.
type openedPane struct {
	PaneID string `json:"pane_id"`
	TabID  string `json:"tab_id"`
}

// pluginPaneOpen asks herdr to open one of this plugin's [[panes]]
// entrypoints and returns the created pane/tab ids (zero for popup
// placement — popups are not panes). env is forwarded into the pane
// process's environment; width/height are popup-only (herdr rejects them
// elsewhere) and may be cells ("120") or a percentage ("95%").
func (c *herdrClient) pluginPaneOpen(pluginID, entrypoint, placement string, focus bool, env map[string]string, width, height string) (*openedPane, error) {
	params := map[string]any{
		"plugin_id":  pluginID,
		"entrypoint": entrypoint,
		"placement":  placement,
		"focus":      focus,
	}
	if len(env) > 0 {
		params["env"] = env
	}
	if width != "" {
		params["width"] = width
	}
	if height != "" {
		params["height"] = height
	}
	var out struct {
		PluginPane struct {
			Pane openedPane `json:"pane"`
		} `json:"plugin_pane"`
	}
	if err := c.call("plugin.pane.open", params, &out); err != nil {
		return nil, err
	}
	return &out.PluginPane.Pane, nil
}

// pluginPaneFocus focuses an existing plugin pane (tab switch included).
func (c *herdrClient) pluginPaneFocus(paneID string) error {
	return c.call("plugin.pane.focus", map[string]any{"pane_id": paneID}, nil)
}

// pluginPaneClose closes a plugin pane (and its tab when it is the only
// pane there).
func (c *herdrClient) pluginPaneClose(paneID string) error {
	return c.call("plugin.pane.close", map[string]any{"pane_id": paneID}, nil)
}

// tabRename sets a tab's display label.
func (c *herdrClient) tabRename(tabID, label string) error {
	return c.call("tab.rename", map[string]any{"tab_id": tabID, "label": label}, nil)
}
