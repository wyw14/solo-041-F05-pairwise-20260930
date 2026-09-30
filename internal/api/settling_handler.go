package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/wyw14/cry-97/internal/model"
	"github.com/wyw14/cry-97/internal/settling"
)

type blanketRequest struct {
	Level float64 `json:"level"`
}

type settlingAdvanceBlocked struct {
	Error   string         `json:"error"`
	Cycle   settling.Cycle `json:"cycle"`
	Reasons []string       `json:"reasons"`
}

func (s *Server) updateBlanket(w http.ResponseWriter, r *http.Request) {
	lineID, err := model.ParseLineID(chi.URLParam(r, "lineID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var input blanketRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	snapshot, err := s.plant.UpdateBlanket(lineID, input.Level)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusAccepted, snapshot)
}

func (s *Server) settlingStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"lines": s.plant.SettlingStatus()})
}

func (s *Server) beginSettling(w http.ResponseWriter, r *http.Request) {
	lineID, err := model.ParseLineID(chi.URLParam(r, "lineID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cycle, err := s.plant.BeginSettling(lineID)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusAccepted, cycle)
}

func (s *Server) advanceSettling(w http.ResponseWriter, r *http.Request) {
	lineID, err := model.ParseLineID(chi.URLParam(r, "lineID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.plant.AdvanceSettling(lineID)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	if result.Blocked {
		writeJSON(w, http.StatusConflict, settlingAdvanceBlocked{
			Error:   "decant delayed: " + strings.Join(result.Reasons, "; "),
			Cycle:   result.Cycle,
			Reasons: result.Reasons,
		})
		return
	}
	writeJSON(w, http.StatusOK, result)
}
