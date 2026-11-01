package domain

import "context"

type PartyRepository interface {
	Save(ctx context.Context, party *Party) error
	FindByID(ctx context.Context, id string) (*Party, error)
	FindByIdentityNumber(ctx context.Context, idNumber string) (*Party, error)
	List(ctx context.Context, filterType string) ([]*Party, error)
}

