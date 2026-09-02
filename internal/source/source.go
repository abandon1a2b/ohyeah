package source

import (
	"context"
	"fmt"

	"github.com/abandon1a2b/ohyeah/internal/model"
)

type Scanner interface {
	Scan(context.Context, model.Source, int64) (model.ScanResult, error)
}

func New(registered model.Source) (Scanner, error) {
	switch registered.Driver {
	case model.DriverCommand:
		return NewCommand(registered)
	default:
		return nil, fmt.Errorf("unsupported collector driver %q", registered.Driver)
	}
}
