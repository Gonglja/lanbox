package httpapi

import "net/http"

// 本文件是尚未实现处理器的临时桩，Task 8–10 会逐个删除对应方法，最后删除整个文件。
func (s *Server) handleListItems(w http.ResponseWriter, r *http.Request)  { s.notImplemented(w) }
func (s *Server) handleCreateItem(w http.ResponseWriter, r *http.Request) { s.notImplemented(w) }
func (s *Server) handleGetItem(w http.ResponseWriter, r *http.Request)    { s.notImplemented(w) }
func (s *Server) handlePatchItem(w http.ResponseWriter, r *http.Request)  { s.notImplemented(w) }
func (s *Server) handleDeleteItem(w http.ResponseWriter, r *http.Request) { s.notImplemented(w) }
func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) { s.notImplemented(w) }
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request)   { s.notImplemented(w) }
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request)     { s.notImplemented(w) }

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
