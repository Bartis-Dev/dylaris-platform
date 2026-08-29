package store

import "time"

// CoreLinkRoute is the durable record of one route-only entry.
//
// Core publishes these straight into Redis as route:<domain> rather than
// through the hub's queue, because the edge reaches the tenant's own link over
// the tenant's own tunnel and no hub row describes that. The Redis value was
// the ONLY copy, which made it the one piece of tenant configuration on the
// platform with no source of truth behind it. This row is that source; Redis is
// a cache of it.
type CoreLinkRoute struct {
	Domain     string
	OwnerID    string
	LinkToken  string
	TargetHost string
	TargetPort int
	CreatedAt  time.Time
}

// UpsertCoreLinkRoute records a route-only entry, or rewrites the one already
// stored for that domain. The domain is the key for the same reason it is the
// key in Redis: one address routes to one place.
func (s *PostgresStore) UpsertCoreLinkRoute(r CoreLinkRoute) error {
	_, err := s.db.Exec(`
		INSERT INTO core_link_routes (domain, owner_id, link_token, target_host, target_port)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (domain) DO UPDATE SET
			owner_id = EXCLUDED.owner_id,
			link_token = EXCLUDED.link_token,
			target_host = EXCLUDED.target_host,
			target_port = EXCLUDED.target_port`,
		r.Domain, r.OwnerID, r.LinkToken, r.TargetHost, r.TargetPort)
	return err
}

func (s *PostgresStore) ListCoreLinkRoutes() ([]CoreLinkRoute, error) {
	rows, err := s.db.Query(`
		SELECT domain, owner_id, link_token, target_host, target_port, created_at
		FROM core_link_routes ORDER BY domain`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CoreLinkRoute
	for rows.Next() {
		var r CoreLinkRoute
		if err := rows.Scan(&r.Domain, &r.OwnerID, &r.LinkToken, &r.TargetHost, &r.TargetPort, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PostgresStore) DeleteCoreLinkRoute(domain string) error {
	_, err := s.db.Exec(`DELETE FROM core_link_routes WHERE domain = $1`, domain)
	return err
}
