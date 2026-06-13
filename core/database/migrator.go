package database

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/go-sql-driver/mysql" // Required for import
)

// MigrateFromMySQL copies data from MySQL to Postgres
func MigrateFromMySQL(postgresDB *sql.DB, mysqlDSN string) error {
	log.Println("Starting migration from MySQL to PostgreSQL...")

	mysqlDB, err := sql.Open("mysql", mysqlDSN)
	if err != nil {
		return fmt.Errorf("mysql connection failed: %v", err)
	}
	defer mysqlDB.Close()

	if err := mysqlDB.Ping(); err != nil {
		return fmt.Errorf("mysql ping failed: %v", err)
	}

	// 1. Users
	// NOTE: This legacy MySQL→Postgres migrator predates the current UUID
	// schema (users.id is now UUID, public_id was dropped). It is kept only as a
	// historical reference and would need a rewrite before running again —
	// the SERIAL id + public_id assumptions no longer match the live schema.
	log.Println("... migrating Users (LEGACY — no-op against Phase 15 schema)")
	rows, err := mysqlDB.Query("SELECT id, username, password, COALESCE(minecraft_username,''), is_admin, created_at FROM users")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id int
			var u, p, mc, cr string
			var adm bool
			rows.Scan(&id, &u, &p, &mc, &adm, &cr)
			// users.id is now UUID with a DEFAULT — let it auto-generate.
			postgresDB.Exec(`INSERT INTO users (username, password, minecraft_username, is_admin, created_at)
				VALUES ($1, $2, $3, $4, $5) ON CONFLICT (username) DO NOTHING`, u, p, mc, adm, cr)
		}
	}

	// 2. Nodes
	log.Println("... migrating Nodes")
	rowsN, err := mysqlDB.Query("SELECT id, name, address, token, status, is_local, COALESCE(tags, '') FROM nodes")
	if err == nil {
		defer rowsN.Close()
		for rowsN.Next() {
			var id int
			var n, a, t, s, tags string
			var l bool
			rowsN.Scan(&id, &n, &a, &t, &s, &l, &tags)
			postgresDB.Exec(`INSERT INTO nodes (id, name, address, token, status, is_local, tags) 
				VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (id) DO NOTHING`, id, n, a, t, s, l, tags)
		}
		postgresDB.Exec("SELECT setval('nodes_id_seq', (SELECT MAX(id) FROM nodes))")
	}

	// 3. Servers
	log.Println("... migrating Servers")
	rowsS, err := mysqlDB.Query("SELECT id, uuid, name, node_id, owner_id, game_image, port, memory, COALESCE(start_command, ''), status FROM servers")
	if err == nil {
		defer rowsS.Close()
		for rowsS.Next() {
			var id, nid, oid, pt, mem int
			var uuid, nm, img, cmd, st string
			rowsS.Scan(&id, &uuid, &nm, &nid, &oid, &img, &pt, &mem, &cmd, &st)
			postgresDB.Exec(`INSERT INTO servers (id, uuid, name, node_id, owner_id, game_image, port, memory, start_command, status) 
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) ON CONFLICT (id) DO NOTHING`, id, uuid, nm, nid, oid, img, pt, mem, cmd, st)
		}
		postgresDB.Exec("SELECT setval('servers_id_seq', (SELECT MAX(id) FROM servers))")
	}

	// 4. Modules (FIX: COALESCE for url)
	log.Println("... migrating Modules")
	// FIX: COALESCE(url, '') prevents NULL errors
	rowsM, err := mysqlDB.Query("SELECT id, name, type, icon, COALESCE(url, ''), is_enabled, is_system, position FROM modules")
	if err == nil {
		defer rowsM.Close()
		for rowsM.Next() {
			var id, pos int
			var name, typ, icon, url string
			var en, sys bool

			// Scan no longer breaks because URL is now guaranteed to be a string
			if err := rowsM.Scan(&id, &name, &typ, &icon, &url, &en, &sys, &pos); err != nil {
				log.Printf("Error scanning module %d: %v", id, err)
				continue
			}

			postgresDB.Exec(`INSERT INTO modules (id, name, type, icon, url, is_enabled, is_system, position) 
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (id) DO NOTHING`,
				id, name, typ, icon, url, en, sys, pos)
		}
		postgresDB.Exec("SELECT setval('modules_id_seq', (SELECT MAX(id) FROM modules))")
	}

	log.Println("Migration completed!")
	return nil
}
