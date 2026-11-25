package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/nyakooo/meldmail/internal/mail"
)

func (s *Server) configTransferSlot(w http.ResponseWriter) bool {
	select {
	case s.configRequests <- struct{}{}:
		return true
	default:
		http.Error(w, "配置操作正在进行，请稍后重试", http.StatusTooManyRequests)
		return false
	}
}
func configRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		http.Error(w, "配置文件或请求格式无效", 400)
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "配置文件或请求格式无效", 400)
		return false
	}
	return true
}
func (s *Server) exportConfig(w http.ResponseWriter, r *http.Request) {
	if !s.configTransferSlot(w) {
		return
	}
	defer func() { <-s.configRequests }()
	var input struct {
		Password string `json:"password"`
	}
	if !configRequest(w, r, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	bundle, err := s.mail.ExportConfig(ctx, input.Password)
	if err != nil {
		http.Error(w, "配置导出失败，请确认密码至少 12 个字符且已有邮箱配置", 400)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="meldmail-config.json"`)
	writeJSON(w, bundle)
}
func (s *Server) importConfig(w http.ResponseWriter, r *http.Request) {
	if !s.configTransferSlot(w) {
		return
	}
	defer func() { <-s.configRequests }()
	var input struct {
		Password string            `json:"password"`
		Bundle   mail.ConfigBundle `json:"bundle"`
	}
	if !configRequest(w, r, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	result, err := s.mail.ImportConfig(ctx, input.Password, input.Bundle)
	if err != nil {
		if errors.Is(err, mail.ErrInvalidConfig) {
			http.Error(w, mail.ErrInvalidConfig.Error(), 400)
		} else {
			http.Error(w, "配置导入失败，未写入任何配置；请检查文件与邮箱字段", 400)
		}
		return
	}
	writeJSON(w, result)
}
