package auditlog

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"konakpro/internal/db"
)

type Actor struct {
	ID    *int64
	Label string
}

func System(label string) Actor { return Actor{Label: label} }

func Write(ctx context.Context, q *db.Queries, actor Actor, action, entity string, id int64, before, after any) error {
	return WriteRef(ctx, q, actor, action, entity, strconv.FormatInt(id, 10), before, after)
}

func WriteRef(ctx context.Context, q *db.Queries, actor Actor, action, entity, ref string, before, after any) error {
	beforeJSON, err := encode(before)
	if err != nil {
		return err
	}
	afterJSON, err := encode(after)
	if err != nil {
		return err
	}
	return q.InsertAudit(ctx, db.InsertAuditParams{
		ActorID:    actor.ID,
		ActorLabel: actor.Label,
		Action:     action,
		Entity:     entity,
		EntityID:   ref,
		BeforeData: beforeJSON,
		AfterData:  afterJSON,
	})
}

func encode(value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("denetim kaydı: %w", err)
	}
	return raw, nil
}
