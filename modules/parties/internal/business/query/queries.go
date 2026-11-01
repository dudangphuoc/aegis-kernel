package query

import (
	"context"

	"github.com/dudangphuoc/aegis-kernel/modules/parties/internal/domain"
)

type ListPartiesHandler struct {
	repo domain.PartyRepository
}

func NewListPartiesHandler(repo domain.PartyRepository) *ListPartiesHandler {
	return &ListPartiesHandler{repo: repo}
}

func (h *ListPartiesHandler) Handle(ctx context.Context, filterType string) ([]*domain.Party, error) {
	return h.repo.List(ctx, filterType)
}

type FindByIdentityHandler struct {
	repo domain.PartyRepository
}

func NewFindByIdentityHandler(repo domain.PartyRepository) *FindByIdentityHandler {
	return &FindByIdentityHandler{repo: repo}
}

func (h *FindByIdentityHandler) Handle(ctx context.Context, idNumber string) (*domain.Party, error) {
	return h.repo.FindByIdentityNumber(ctx, idNumber)
}

