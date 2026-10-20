package mail

import "context"

// Queue adds messages to the outbox and wakes this process's runner.
type Queue struct {
	Store interface {
		Enqueue(ctx context.Context, m Message) error
	}
	Runner *Runner // nil when this process runs no mail workers
}

// Enqueue adds m to the outbox and, if that worked, wakes the runner.
func (q *Queue) Enqueue(ctx context.Context, m Message) error {
	if err := q.Store.Enqueue(ctx, m); err != nil {
		return err
	}
	q.Runner.Wake()
	return nil
}
