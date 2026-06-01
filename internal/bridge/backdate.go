package bridge

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rricajos/qrsgen/internal/metrics"
)

// Backdater corrige el `created_at` de los mensajes ya posteados a
// Chatwoot, usando el `external_created_at` que qrsgen embebe en cada
// POST (desde v0.46.0) dentro de `content_attributes`.
//
// Por qué hace falta: Chatwoot ignora silenciosamente el `created_at`
// suministrado vía `api_access_token` por seguridad (solo un super-
// admin user-token puede backdatear). El campo igual se guarda en
// `content_attributes->>'external_created_at'`, así que qrsgen puede
// volver más tarde y patchear el `created_at` real con un UPDATE
// directo a la DB de Chatwoot. Reorden cronológico correcto post-hoc.
//
// Activación: requiere CHATWOOT_DB_URL configurado. Sin esa env el
// worker no arranca y los mensajes importados quedan con timestamp
// "now". Opt-in deliberado — la feature acopla qrsgen al schema de
// `messages` en Chatwoot.
//
// Idempotente: el WHERE solo selecciona rows donde `created_at` aún
// diverge del `external_created_at` por más de la tolerancia, así
// que sucesivos ticks no rehacen trabajo.
//
// v0.54.0.
type Backdater struct {
	pool         *pgxpool.Pool
	logger       *slog.Logger
	interval     time.Duration
	batchSize    int
	toleranceSec int
}

// NewBackdater construye un Backdater. El pool debe apuntar a la DB
// `chatwoot` (no la de qrsgen). interval, batchSize, toleranceSec se
// validan: si vienen ≤0 se usan defaults conservadores.
func NewBackdater(pool *pgxpool.Pool, logger *slog.Logger, interval time.Duration, batchSize, toleranceSec int) *Backdater {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	if toleranceSec <= 0 {
		toleranceSec = 5
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Backdater{
		pool:         pool,
		logger:       logger,
		interval:     interval,
		batchSize:    batchSize,
		toleranceSec: toleranceSec,
	}
}

// Run bloquea hasta que ctx se cancele. Ticks cada `interval`. Errores
// transitorios se loguean a WARN; el worker no aborta.
func (b *Backdater) Run(ctx context.Context) {
	b.logger.Info("backdate worker started",
		"interval", b.interval, "batch_size", b.batchSize, "tolerance_sec", b.toleranceSec)
	// Primer tick inmediato — útil tras un restart con backlog acumulado.
	if n, err := b.tick(ctx); err != nil {
		b.logger.Warn("backdate tick error", "err", err)
	} else if n > 0 {
		b.logger.Info("backdate updated", "rows", n)
	}
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			b.logger.Info("backdate worker stopping", "reason", ctx.Err())
			return
		case <-ticker.C:
			n, err := b.tick(ctx)
			if err != nil {
				metrics.RealtimeEventsTotal.WithLabelValues("backdate", "error", "all").Inc()
				b.logger.Warn("backdate tick error", "err", err)
				continue
			}
			if n > 0 {
				metrics.RealtimeEventsTotal.WithLabelValues("backdate", "ok", "all").Add(float64(n))
				b.logger.Info("backdate updated", "rows", n)
			}
		}
	}
}

// tick ejecuta un UPDATE batch. Devuelve el número de filas actualizadas.
//
// La query se construye con CTE para limitar el batch sin que el LIMIT
// dependa de orden volátil (id DESC es estable). Solo toca rows cuyo
// `created_at` está al menos `toleranceSec` segundos por encima del
// `external_created_at` — protege contra micro-jitter y evita loops.
//
// Nota: Chatwoot define `messages.content_attributes` como `json` (no
// `jsonb`), así que el operador `?` y `->>` requieren cast explícito
// a jsonb. v0.54.1 — corrección del bug descubierto en el primer
// intento de activación; v0.54.0 fallaba en cada tick con "operator
// does not exist: json ?".
func (b *Backdater) tick(ctx context.Context) (int64, error) {
	const q = `
		WITH stale AS (
			SELECT id
			FROM messages
			WHERE (content_attributes::jsonb) ? 'external_created_at'
			  AND created_at > to_timestamp(((content_attributes::jsonb)->>'external_created_at')::bigint)
			                    + make_interval(secs => $1::int)
			ORDER BY id DESC
			LIMIT $2
		)
		UPDATE messages m
		SET created_at = to_timestamp(((m.content_attributes::jsonb)->>'external_created_at')::bigint)
		FROM stale
		WHERE m.id = stale.id
	`
	tag, err := b.pool.Exec(ctx, q, b.toleranceSec, b.batchSize)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
