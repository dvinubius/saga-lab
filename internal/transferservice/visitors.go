package transferservice

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dvinubius/saga-lab/internal/visitor"
	"github.com/dvinubius/saga-lab/internal/web"
	"github.com/jackc/pgx/v5"
)

func (s *Service) visitors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if cookie, err := r.Cookie(visitor.CookieName); err == nil && validToken(cookie.Value) {
			token = cookie.Value
		}
		if token == "" {
			var err error
			if token, err = s.newVisitor(w); err != nil {
				return
			}
		}
		id := visitorID(token)
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

func (s *Service) newVisitor(w http.ResponseWriter) (string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		s.logger.Error("generate visitor", "error", err)
		http.Error(w, "The page could not be served.", http.StatusInternalServerError)
		return "", err
	}
	token := hex.EncodeToString(random[:])
	http.SetCookie(w, &http.Cookie{Name: visitor.CookieName, Value: token, HttpOnly: true, Secure: s.secureCookie, SameSite: http.SameSiteLaxMode, Path: "/", MaxAge: 365 * 24 * 60 * 60, Expires: time.Now().AddDate(1, 0, 0)})
	return token, nil
}

func validToken(token string) bool {
	_, err := hex.DecodeString(token)
	return len(token) == 64 && err == nil
}

func visitorID(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
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

var errTransferInProgress = errors.New("a transfer is still in progress")

func (s *Service) postResetForm(w http.ResponseWriter, r *http.Request) {
	old := visitor.ID(r.Context())
	err := s.forgetVisitor(r.Context(), old)
	if errors.Is(err, errTransferInProgress) {
		s.renderHome(w, r, http.StatusConflict, homePage{ResetRefused: true})
		return
	}
	if err != nil {
		s.logger.Error("forget visitor", "error", err)
	}
	if _, err := s.newVisitor(w); err != nil {
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Service) forgetVisitor(ctx context.Context, id string) error {
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		holder, err := lockSlot(ctx, tx)
		if err != nil {
			return err
		}
		var busy bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM transfers WHERE visitor_id = $1 AND (status = ANY($2) OR transfer_id = $3))`, id, pendingStatuses, holder).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return errTransferInProgress
		}
		for _, statement := range []string{
			`DELETE FROM transfer_history WHERE transfer_id IN (SELECT transfer_id FROM transfers WHERE visitor_id = $1)`,
			`DELETE FROM transfers WHERE visitor_id = $1`,
			`DELETE FROM visitors WHERE visitor_id = $1`,
		} {
			if _, err := tx.Exec(ctx, statement, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.bankA.closeAccount(ctx, id); err != nil {
		return err
	}
	return s.bankB.closeAccount(ctx, id)
}
