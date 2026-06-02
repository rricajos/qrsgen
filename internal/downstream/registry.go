package downstream

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rricajos/qrsgen/internal/tenant"
)

// Router es la interfaz mínima que bridge.{Incoming,Outgoing} usan para
// obtener el `*Client` adecuado para una instancia. Tanto `*Client`
// (single-downstream) como `*Registry` (multi-downstream) la
// implementan, así que el callsite no necesita saber cuál es.
type Router interface {
	For(ctx context.Context, instance string) *Client
	// OwnerTagFor devuelve el owner_tag asociado a esta instancia, o ""
	// si no tiene tenant configurado (single-downstream, instance sin
	// owner_tag, o tenant aún no mapeado). Cacheado con TTL en Registry.
	OwnerTagFor(ctx context.Context, instance string) string
}

// For implementación trivial para single-downstream: el cliente se
// devuelve a sí mismo. Permite que código que ya tenía un `*Client`
// directo siga funcionando sin cambios al adoptar la interface Router.
func (c *Client) For(_ context.Context, _ string) *Client { return c }

// OwnerTagFor single-downstream: devuelve "" siempre. No hay tenants
// configurados, todo va al cliente global.
func (c *Client) OwnerTagFor(_ context.Context, _ string) string { return "" }

// OwnerTagFor devuelve el owner_tag cacheado de una instancia, o "" si
// no tiene. Idéntico al lookup interno que `For` usa para route — lo
// exponemos como método público para que los callsites de métricas
// puedan etiquetar contadores por tenant sin volver a hacer la query.
func (r *Registry) OwnerTagFor(ctx context.Context, instance string) string {
	if r == nil {
		return ""
	}
	return r.ownerTagFor(ctx, instance)
}

// Registry resuelve el `*Client` adecuado para una instancia dada,
// considerando su `owner_tag` y el mapeo en bridge_tenant. Si la
// instancia no tiene owner_tag o el owner_tag no existe en bridge_tenant,
// devuelve el fallback global (configurado vía env DOWNSTREAM_*).
//
// Mantiene una pool interna de `*Client` por owner_tag para reusar las
// conexiones HTTP (cada Client tiene su propio http.Client). Si la
// config del tenant cambia (Set), invalida la entrada cached.
type Registry struct {
	pool     *pgxpool.Pool
	tenants  *tenant.Resolver
	fallback *Client

	// tenantOpts se aplican a cada Client construido on-demand para un
	// tenant. Permite que las opts globales (auth header/scheme/path
	// prefix configurados vía env DOWNSTREAM_*) se propaguen a todos los
	// tenants. Hoy v0.65.0 no hay per-tenant override de estos campos —
	// si lo necesitas, abre issue.
	tenantOpts []Option

	mu      sync.RWMutex
	clients map[string]*Client // owner_tag → Client

	// instanceTagTTL: cuánto cacheamos la lookup instance → owner_tag.
	// Lookup DB por cada mensaje sería costoso; con TTL corto evitamos
	// inconsistencias prolongadas tras cambios PATCH a la instancia.
	instanceTagTTL time.Duration

	insMu       sync.RWMutex
	instanceTag map[string]instanceTagEntry
}

type instanceTagEntry struct {
	ownerTag string
	expires  time.Time
}

// NewRegistry crea el registry con fallback y resolver de tenants.
// instanceTagTTL=30s es razonable: las instancias raramente cambian de
// owner_tag, y un retraso de 30s en propagar cambios es aceptable.
// `tenantOpts` se aplican a cada Client construido on-demand para un
// tenant (típicamente las mismas opts que el fallback recibió).
func NewRegistry(pool *pgxpool.Pool, tenants *tenant.Resolver, fallback *Client, tenantOpts ...Option) *Registry {
	return &Registry{
		pool:           pool,
		tenants:        tenants,
		fallback:       fallback,
		tenantOpts:     tenantOpts,
		clients:        map[string]*Client{},
		instanceTag:    map[string]instanceTagEntry{},
		instanceTagTTL: 30 * time.Second,
	}
}

// For devuelve el client downstream adecuado para una instancia. Nunca
// devuelve nil — siempre cae al fallback si no encuentra tenant.
func (r *Registry) For(ctx context.Context, instance string) *Client {
	if r == nil {
		return nil
	}
	if r.fallback == nil {
		return nil
	}
	ownerTag := r.ownerTagFor(ctx, instance)
	if ownerTag == "" {
		return r.fallback
	}

	r.mu.RLock()
	c, ok := r.clients[ownerTag]
	r.mu.RUnlock()
	if ok {
		return c
	}

	cfg, err := r.tenants.Get(ctx, ownerTag)
	if err != nil {
		// Tenant no configurado para este owner_tag → fallback global.
		return r.fallback
	}
	client := New(cfg.DownstreamBaseURL, cfg.DownstreamAPIToken, cfg.DownstreamAccountID, r.tenantOpts...)
	r.mu.Lock()
	r.clients[ownerTag] = client
	r.mu.Unlock()
	return client
}

// InboxIDFor devuelve el inbox_id que el integrador configuró para el
// tenant de esta instancia, o 0 si no hay (caer al global).
func (r *Registry) InboxIDFor(ctx context.Context, instance string) int {
	if r == nil || r.tenants == nil {
		return 0
	}
	ownerTag := r.ownerTagFor(ctx, instance)
	if ownerTag == "" {
		return 0
	}
	cfg, err := r.tenants.Get(ctx, ownerTag)
	if err != nil {
		return 0
	}
	return cfg.DownstreamInboxID
}

// InvalidateTenant evict el cliente cacheado de un owner_tag. Llamar
// tras un Set/Delete del tenant para que la próxima request use la
// nueva config.
func (r *Registry) InvalidateTenant(ownerTag string) {
	r.mu.Lock()
	delete(r.clients, ownerTag)
	r.mu.Unlock()
}

// ownerTagFor consulta el owner_tag de una instancia, con cache TTL.
func (r *Registry) ownerTagFor(ctx context.Context, instance string) string {
	if instance == "" {
		return ""
	}
	now := time.Now()
	r.insMu.RLock()
	entry, ok := r.instanceTag[instance]
	r.insMu.RUnlock()
	if ok && now.Before(entry.expires) {
		return entry.ownerTag
	}
	var ownerTag string
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(owner_tag, '') FROM bridge_instance WHERE name=$1`,
		instance,
	).Scan(&ownerTag)
	if err != nil {
		if !errIsNoRows(err) {
			// log? — no tenemos logger aquí. Best-effort.
			return ""
		}
		ownerTag = ""
	}
	r.insMu.Lock()
	r.instanceTag[instance] = instanceTagEntry{ownerTag: ownerTag, expires: now.Add(r.instanceTagTTL)}
	r.insMu.Unlock()
	return ownerTag
}

func errIsNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
