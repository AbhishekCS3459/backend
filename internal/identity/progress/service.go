package progress

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

type Service interface {
	Get(ctx context.Context, userID uuid.UUID) (*View, error)
	Save(ctx context.Context, userID uuid.UUID, draft Draft) (*View, error)
}

type service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return &service{repo: repo}
}

func (s *service) Get(ctx context.Context, userID uuid.UUID) (*View, error) {
	row, err := s.repo.FindByUserID(ctx, userID)
	if err == ErrNotFound {
		steps, overall, current := Evaluate(Draft{})
		return &View{Status: overall, CurrentStep: current, Steps: steps, Data: nil}, nil
	}
	if err != nil {
		return nil, err
	}
	return viewFrom(row)
}

func (s *service) Save(ctx context.Context, userID uuid.UUID, draft Draft) (*View, error) {
	steps, _, _ := Evaluate(draft)
	if steps[len(steps)-1].Status != StatusCompleted {
		draft.Submitted = false
		steps, _, _ = Evaluate(draft)
	}
	payload, err := json.Marshal(draft)
	if err != nil {
		return nil, err
	}
	_, overall, _ := Evaluate(draft)
	row, err := s.repo.Save(ctx, userID, overall, payload)
	if err != nil {
		return nil, err
	}
	return viewFrom(row)
}

func viewFrom(row *Record) (*View, error) {
	var draft Draft
	if len(row.Payload) > 0 {
		if err := json.Unmarshal([]byte(row.Payload), &draft); err != nil {
			return nil, err
		}
	}
	steps, overall, current := Evaluate(draft)
	updated := row.UpdatedAt
	return &View{
		Status:      overall,
		CurrentStep: current,
		Steps:       steps,
		Data:        &draft,
		UpdatedAt:   &updated,
	}, nil
}
