package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
)

func TestHerdrPaneGraphicsProtocol(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "herdr.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	requests := make(chan request, 3)
	serverErr := make(chan error, 1)
	go func() {
		for i := 0; i < 3; i++ {
			conn, err := ln.Accept()
			if err != nil {
				serverErr <- err
				return
			}
			var req request
			if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
				conn.Close()
				serverErr <- err
				return
			}
			requests <- req
			result := map[string]any{}
			if req.Method == "pane.graphics.info" {
				result = map[string]any{
					"type": "pane_graphics_info", "cell_width_px": 9, "cell_height_px": 18,
				}
			}
			if err := json.NewEncoder(conn).Encode(map[string]any{"id": req.ID, "result": result}); err != nil {
				conn.Close()
				serverErr <- err
				return
			}
			conn.Close()
		}
		serverErr <- nil
	}()

	c := &herdrClient{socketPath: socketPath}
	metrics, err := c.paneGraphicsInfo("pane-1")
	if err != nil || metrics.CellWidthPX != 9 || metrics.CellHeightPX != 18 {
		t.Fatalf("metrics=%+v err=%v", metrics, err)
	}
	placement := AvatarCell{X: 2, Y: 3, Cols: 4, Rows: 2}
	if err := c.paneGraphicsSet("pane-1", []byte("png"), 36, 36, placement); err != nil {
		t.Fatal(err)
	}
	if err := c.paneGraphicsClear("pane-1"); err != nil {
		t.Fatal(err)
	}

	infoReq, setReq, clearReq := <-requests, <-requests, <-requests
	if infoReq.Method != "pane.graphics.info" || infoReq.Params["pane_id"] != "pane-1" {
		t.Errorf("info request = %+v", infoReq)
	}
	if setReq.Method != "pane.graphics.set" || setReq.Params["format"] != "png" {
		t.Fatalf("set request = %+v", setReq)
	}
	if setReq.Params["data_base64"] != base64.StdEncoding.EncodeToString([]byte("png")) {
		t.Errorf("data_base64 = %v", setReq.Params["data_base64"])
	}
	gotPlacement := setReq.Params["placement"].(map[string]any)
	if gotPlacement["viewport_col"] != float64(2) || gotPlacement["grid_rows"] != float64(2) {
		t.Errorf("placement = %+v", gotPlacement)
	}
	if clearReq.Method != "pane.graphics.clear" {
		t.Errorf("clear request = %+v", clearReq)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}
