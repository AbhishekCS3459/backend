package catalog

import "context"

type Service interface {
	List(ctx context.Context) ([]Category, error)
}

type service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return &service{repo: repo}
}

func (s *service) List(ctx context.Context) ([]Category, error) {
	return s.repo.List(ctx)
}
