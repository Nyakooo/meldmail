package mail

import (
	"context"
	"log/slog"

	"github.com/nyakooo/meldmail/internal/logging"
)

type BackgroundActivity struct {
	Syncing  int           `json:"syncing"`
	Prefetch *BodyPrefetch `json:"prefetch"`
}

type BodyPrefetch struct {
	AccountID int64  `json:"account_id"`
	Subject   string `json:"subject"`
}

func (s *Service) BackgroundActivity() BackgroundActivity {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	return s.activity
}

func (s *Service) updateBackgroundActivity(update func(*BackgroundActivity)) {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	update(&s.activity)
	if err := s.Events.Publish("background_activity", s.activity); err != nil {
		slog.Default().LogAttrs(context.Background(), slog.LevelWarn, "mail background activity update failed", logging.Failure(err)...)
	}
}

func (s *Service) syncInBackground(ctx context.Context, accountID int64) (int, error) {
	s.updateBackgroundActivity(func(activity *BackgroundActivity) { activity.Syncing++ })
	defer s.updateBackgroundActivity(func(activity *BackgroundActivity) { activity.Syncing-- })
	return s.Sync(ctx, accountID)
}
