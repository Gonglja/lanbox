package httpapi

import "net/http"

// 本文件是尚未实现处理器的临时桩，Task 10 会删除整个文件。
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) { s.notImplemented(w) }
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	s.notImplemented(w)
}
func (s *Server) handleSetUserPassword(w http.ResponseWriter, r *http.Request) {
	s.notImplemented(w)
}
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) { s.notImplemented(w) }

func (s *Server) notImplemented(w http.ResponseWriter) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "尚未实现")
}
