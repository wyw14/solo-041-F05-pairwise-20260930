package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/wyw14/cry-97/internal/model"
	"github.com/wyw14/cry-97/internal/settling"
)

type blanketRequest struct {
	Level float64 `json:"level"`
}

type holdResponse struct {
	Error       string   `json:"error"`
	Phase       string   `json:"phase"`
	HoldReasons []string `json:"hold_reasons"`
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
	cycle, err := s.plant.AdvanceSettling(lineID)
	if err != nil {
		var hold *settling.HoldError
		if errors.As(err, &hold) {
			writeJSON(w, http.StatusConflict, holdResponse{
				Error:       hold.Error(),
				Phase:       string(settling.PhaseSettle),
				HoldReasons: hold.HoldReasons(),
			})
			return
		}
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, cycle)
}
