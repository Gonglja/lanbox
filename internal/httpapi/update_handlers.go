package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"lanbox/internal/updater"
)

// UpdateManager 是更新能力的抽象（*updater.Manager 实现它，测试可注入假实现）。
type UpdateManager interface {
	State() updater.State
	Check(ctx context.Context) (updater.State, error)
	Apply(ctx context.Context, version string) (updater.State, error)
	Rollback() (updater.State, error)
}

// currentUpdate 返回当前生效的更新管理器；nil 表示该宿主不支持在线更新。
func (s *Server) currentUpdate() UpdateManager {
	if s.update == nil {
		return nil
	}
	return s.update()
}

// handleGetUpdate 只读缓存，不联网（状态页每 5 秒轮询它）。
func (s *Server) handleGetUpdate(w http.ResponseWriter, r *http.Request) {
	m := s.currentUpdate()
	if m == nil {
		writeError(w, http.StatusNotImplemented, "not_supported", "当前运行环境不支持在线更新")
		return
	}
	writeJSON(w, http.StatusOK, m.State())
}

// handleCheckUpdate 立即检查更新。
func (s *Server) handleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	m := s.currentUpdate()
	if m == nil {
		writeError(w, http.StatusNotImplemented, "not_supported", "当前运行环境不支持在线更新")
		return
	}
	st, err := m.Check(r.Context())
	if err != nil {
		s.writeUpdateErr(w, err, "检查更新失败")
		return
	}
	s.broadcast("update", st)
	writeJSON(w, http.StatusOK, st)
}

// handleApplyUpdate 下载 → 校验 → 替换 → 请求重启。
func (s *Server) handleApplyUpdate(w http.ResponseWriter, r *http.Request) {
	m := s.currentUpdate()
	if m == nil || s.restartDisabled {
		writeError(w, http.StatusNotImplemented, "not_supported", "当前运行环境不支持在线升级（无法重启进程）")
		return
	}
	var in struct {
		Version string `json:"version"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || in.Version == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体需要 {\"version\":\"1.2.3\"}")
		return
	}
	st, err := m.Apply(r.Context(), in.Version)
	if err != nil {
		s.writeUpdateErr(w, err, "升级失败")
		return
	}
	s.log.Printf("info: %s 升级到 %s，正在重启", userFrom(r).Username, st.Pending)
	s.broadcast("update", st)
	writeJSON(w, http.StatusAccepted, st)
}

// handleRollbackUpdate 把 .old 备份换回来并重启。
func (s *Server) handleRollbackUpdate(w http.ResponseWriter, r *http.Request) {
	m := s.currentUpdate()
	if m == nil || s.restartDisabled {
		writeError(w, http.StatusNotImplemented, "not_supported", "当前运行环境不支持在线回滚")
		return
	}
	st, err := m.Rollback()
	if err != nil {
		s.writeUpdateErr(w, err, "回滚失败")
		return
	}
	s.log.Printf("info: %s 回滚到上一版本，正在重启", userFrom(r).Username)
	s.broadcast("update", st)
	writeJSON(w, http.StatusAccepted, st)
}

// writeUpdateErr 把 updater 的错误翻译成状态码 + 人话。
func (s *Server) writeUpdateErr(w http.ResponseWriter, err error, prefix string) {
	var ue *updater.Error
	switch {
	case errors.Is(err, updater.ErrNotSupported):
		writeError(w, http.StatusNotImplemented, "not_supported", err.Error())
	case errors.Is(err, updater.ErrBusy):
		writeError(w, http.StatusConflict, "update_busy", err.Error())
	case errors.Is(err, updater.ErrVersionMismatch):
		writeError(w, http.StatusConflict, "version_mismatch", err.Error())
	case errors.Is(err, updater.ErrNoBackup):
		writeError(w, http.StatusConflict, "no_backup", "没有可回滚的备份（这个版本不是通过在线升级装的）")
	case errors.Is(err, updater.ErrNoAsset):
		writeError(w, http.StatusBadGateway, "no_asset", prefix+"："+err.Error())
	case errors.As(err, &ue) && ue.Stage == "permission":
		writeError(w, http.StatusInternalServerError, "update_permission", prefix+"："+err.Error())
	case errors.As(err, &ue):
		writeError(w, http.StatusBadGateway, "update_"+ue.Stage, prefix+"："+err.Error())
	default:
		writeError(w, http.StatusBadGateway, "update_failed", prefix+"："+err.Error())
	}
}
