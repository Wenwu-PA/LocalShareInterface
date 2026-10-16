package server

import (
	"context"
	"net/http"
	"time"
)

// Summarize in SQLite without allocating the device list or probing interfaces.
func (s *Server) deviceSummary(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	var result struct {
		Total        int64  `json:"total"`
		Online       int64  `json:"online"`
		Offline      int64  `json:"offline"`
		Unknown      int64  `json:"unknown"`
		Unrecognized int64  `json:"unrecognized"`
		LastScan     int64  `json:"last_scan"`
		Enabled      bool   `json:"enabled"`
		Error        string `json:"error"`
	}
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(status='online'),0),COALESCE(SUM(status='offline'),0),COALESCE(SUM(status NOT IN ('online','offline')),0),COALESCE(SUM(known=0),0),(SELECT last_scan FROM discovery_config WHERE id=1),(SELECT enabled FROM discovery_config WHERE id=1),(SELECT last_error FROM discovery_config WHERE id=1) FROM devices`).Scan(&result.Total, &result.Online, &result.Offline, &result.Unknown, &result.Unrecognized, &result.LastScan, &result.Enabled, &result.Error)
	if err != nil {
		s.dbError(w, err)
		return
	}
	jsonResponse(w, 200, result)
}
