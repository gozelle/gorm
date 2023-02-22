package gorm

import (
	"context"
	"fmt"
)

const dbContextKey = "GORM_DB_CMHenW3d"

func NewBaseDal(db *DB) *BaseDal {
	return &BaseDal{db: db}
}

type BaseDal struct {
	db *DB
}

func ContextWithDB(ctx context.Context, db *DB) context.Context {
	return context.WithValue(ctx, dbContextKey, db)
}

func (m *BaseDal) DB(ctx context.Context) (*DB, error) {
	if v := ctx.Value(dbContextKey); v != nil {
		vv, ok := v.(*DB)
		if !ok {
			return nil, fmt.Errorf("context CONTEXT_DB_KEY expect *gorm.DB")
		}
		return vv, nil
	}
	return m.db, nil
}

func (m *BaseDal) Exec(ctx context.Context, handle func(tx *DB) error) (err error) {
	if handle == nil {
		return
	}
	tx, err := m.DB(ctx)
	if err != nil {
		return
	}
	err = handle(tx)
	return
}
