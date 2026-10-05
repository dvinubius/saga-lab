package transferservice

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dvinubius/saga-lab/internal/visitor"
	"github.com/dvinubius/saga-lab/internal/web"
)

func (s *Service) visitors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := ""
		if cookie, err := r.Cookie(visitor.CookieName); err == nil {
			id = cookie.Value
		}
		if id == "" {
			var random [32]byte
			if _, err := rand.Read(random[:]); err != nil {
				s.logger.Error("generate visitor", "error", err)
				http.Error(w, "The page could not be served.", http.StatusInternalServerError)
				return
			}
			id = hex.EncodeToString(random[:])
			http.SetCookie(w, &http.Cookie{Name: visitor.CookieName, Value: id, HttpOnly: true, SameSite: http.SameSiteLaxMode, Path: "/", MaxAge: 365 * 24 * 60 * 60, Expires: time.Now().AddDate(1, 0, 0)})
		}
		if err := s.openVisitor(r.Context(), id); err != nil {
			s.logger.Error("open visitor accounts", "error", err)
			if strings.HasPrefix(r.URL.Path, "/api/") {
				web.WriteError(w, http.StatusBadGateway, "balances unavailable", s.logger)
			} else {
				http.Error(w, "Balances are temporarily unavailable.", http.StatusBadGateway)
			}
			return
		}
		next.ServeHTTP(w, r.WithContext(visitor.WithID(r.Context(), id)))
	})
}

func (s *Service) openVisitor(ctx context.Context, id string) error {
	var opened bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM visitors WHERE visitor_id = $1)`, id).Scan(&opened); err != nil {
		return fmt.Errorf("read visitor: %w", err)
	}
	if opened {
		return nil
	}
	if err := s.bankA.openAccount(ctx, id); err != nil {
		return err
	}
	if err := s.bankB.openAccount(ctx, id); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO visitors (visitor_id) VALUES ($1) ON CONFLICT (visitor_id) DO NOTHING`, id); err != nil {
		return fmt.Errorf("record visitor: %w", err)
	}
	return nil
}
