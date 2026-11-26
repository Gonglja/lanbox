package httpapi

import (
	"errors"
	"net/http"

	"lanbox/internal/auth"
)

// userDTO 是对外暴露的用户信息（不含任何哈希材料）。
type userDTO struct {
	Username  string `json:"username"`
	IsAdmin   bool   `json:"is_admin"`
	CreatedAt string `json:"created_at"`
}

func toUserDTO(users []*auth.User) []userDTO {
	out := make([]userDTO, 0, len(users))
	for _, u := range users {
		out = append(out, userDTO{
			Username:  u.Username,
			IsAdmin:   u.IsAdmin,
			CreatedAt: u.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	return out
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, toUserDTO(s.auth.List()))
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"is_admin"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.auth.CreateUser(in.Username, in.Password, in.IsAdmin); err != nil {
		switch {
		case errors.Is(err, auth.ErrUserExists):
			writeError(w, http.StatusConflict, "user_exists", "用户名已存在")
		case errors.Is(err, auth.ErrWeakPassword):
			writeError(w, http.StatusBadRequest, "weak_password", err.Error())
		default:
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		}
		return
	}
	s.log.Printf("info: %s 新增用户 %s（管理员=%v）", userFrom(r).Username, in.Username, in.IsAdmin)
	writeJSON(w, http.StatusCreated, userDTO{Username: in.Username, IsAdmin: in.IsAdmin})
}

func (s *Server) handleSetUserPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	name := r.PathValue("name")
	if err := s.auth.SetPassword(name, in.Password); err != nil {
		switch {
		case errors.Is(err, auth.ErrUserNotFound):
			writeError(w, http.StatusNotFound, "user_not_found", "用户不存在")
		case errors.Is(err, auth.ErrWeakPassword):
			writeError(w, http.StatusBadRequest, "weak_password", err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
		}
		return
	}
	s.log.Printf("info: %s 重置了用户 %s 的密码", userFrom(r).Username, name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	err := s.auth.DeleteUser(userFrom(r).Username, name)
	switch {
	case err == nil:
		s.log.Printf("info: %s 删除了用户 %s", userFrom(r).Username, name)
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, auth.ErrUserNotFound):
		writeError(w, http.StatusNotFound, "user_not_found", "用户不存在")
	case errors.Is(err, auth.ErrSelfDelete):
		writeError(w, http.StatusForbidden, "self_delete", "不能删除当前登录的账号")
	case errors.Is(err, auth.ErrLastAdmin):
		writeError(w, http.StatusForbidden, "last_admin", "不能删除最后一个管理员")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}
