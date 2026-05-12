package store

import (
	"database/sql"
	"dylaris-core/models"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// ==========================================
// USERS
// ==========================================

// userSelectCols is the canonical column list for User reads. Includes
// totp_secret + totp_backup_codes which are needed for 2FA verification.
// These two are scrubbed from JSON responses via the json:"-" tag on the model.
const userSelectCols = `id, username, password, COALESCE(email, ''), COALESCE(minecraft_username, ''),
	is_admin, COALESCE(is_2fa_enabled, FALSE), COALESCE(totp_secret, ''), COALESCE(totp_backup_codes::text, '[]'),
	COALESCE(permissions, ''), COALESCE(public_id, ''), created_at`

func scanUser(scan func(dest ...interface{}) error) (*models.User, error) {
	var (
		u         models.User
		createdAt sql.NullTime
	)
	err := scan(&u.ID, &u.Username, &u.Password, &u.Email, &u.MinecraftUsername,
		&u.IsAdmin, &u.Is2FAEnabled, &u.TOTPSecret, &u.TOTPBackupCodes,
		&u.Permissions, &u.PublicID, &createdAt)
	if err != nil {
		return nil, err
	}
	if createdAt.Valid {
		u.CreatedAt = createdAt.Time
	}
	return &u, nil
}

func (s *PostgresStore) GetUserByUsername(username string) (*models.User, error) {
	query := `SELECT ` + userSelectCols + ` FROM users WHERE username = $1`
	return scanUser(s.db.QueryRow(query, username).Scan)
}

func (s *PostgresStore) GetUserByID(id int) (*models.User, error) {
	query := `SELECT ` + userSelectCols + ` FROM users WHERE id = $1`
	return scanUser(s.db.QueryRow(query, id).Scan)
}

func (s *PostgresStore) CreateUser(u *models.User) error {
	// Explicit defaults for totp_* — older deployments may have these columns
	// added via migration without the JSONB default actually being applied to
	// freshly inserted rows in some Postgres versions. Writing the values
	// explicitly avoids relying on the schema default at all.
	query := `INSERT INTO users
		(username, password, email, minecraft_username, is_admin, is_2fa_enabled,
		 totp_secret, totp_backup_codes, permissions, public_id)
		VALUES ($1, $2, $3, $4, $5, $6, '', '[]'::jsonb, $7, $8)
		RETURNING id`
	return s.db.QueryRow(query, u.Username, u.Password, u.Email, u.MinecraftUsername, u.IsAdmin, u.Is2FAEnabled, u.Permissions, u.PublicID).Scan(&u.ID)
}

func (s *PostgresStore) UpdateUser(u *models.User) error {
	query := `UPDATE users SET username = $1, password = $2, email = $3, minecraft_username = $4, is_admin = $5, is_2fa_enabled = $6, permissions = $7, public_id = $8 WHERE id = $9`
	_, err := s.db.Exec(query, u.Username, u.Password, u.Email, u.MinecraftUsername, u.IsAdmin, u.Is2FAEnabled, u.Permissions, u.PublicID, u.ID)
	return err
}

func (s *PostgresStore) UpdateUserPassword(id int, hashedPassword string) error {
	_, err := s.db.Exec("UPDATE users SET password = $1 WHERE id = $2", hashedPassword, id)
	return err
}

func (s *PostgresStore) DeleteUser(id int) error {
	_, err := s.db.Exec("DELETE FROM users WHERE id = $1", id)
	return err
}

func (s *PostgresStore) ListUsers() ([]models.User, error) {
	query := `SELECT ` + userSelectCols + ` FROM users ORDER BY id ASC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []models.User
	for rows.Next() {
		u, err := scanUser(rows.Scan)
		if err != nil {
			// Surface scan failures instead of dropping rows silently —
			// a single bad row used to make freshly-created users vanish
			// from the admin list.
			log.Printf("ListUsers: scan failed: %v", err)
			return nil, err
		}
		users = append(users, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return users, nil
}

func (s *PostgresStore) CountUsers() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	return count, err
}

// SetUserTOTP stores the TOTP secret + hashed backup codes JSON for a user
// and sets is_2fa_enabled accordingly. enabled=true means 2FA is now active.
func (s *PostgresStore) SetUserTOTP(id int, secret, backupCodesJSON string, enabled bool) error {
	_, err := s.db.Exec(
		`UPDATE users SET totp_secret = $1, totp_backup_codes = $2::jsonb, is_2fa_enabled = $3 WHERE id = $4`,
		secret, backupCodesJSON, enabled, id,
	)
	return err
}

// DisableUserTOTP wipes the secret + backup codes and sets is_2fa_enabled to false.
// Used by user-self-disable and admin-reset.
func (s *PostgresStore) DisableUserTOTP(id int) error {
	_, err := s.db.Exec(
		`UPDATE users SET totp_secret = '', totp_backup_codes = '[]'::jsonb, is_2fa_enabled = FALSE WHERE id = $1`,
		id,
	)
	return err
}

// ==========================================
// NODES
// ==========================================

const nodeSelectCols = `id, name, address, token, status, is_local, COALESCE(tags, ''),
	link_enabled, link_instances, COALESCE(link_secret, ''), COALESCE(cpuset_cpus, ''), created_at,
	COALESCE(public_ip, ''), COALESCE(private_ips::text, '[]'), last_seen_at,
	COALESCE(cpu_overcommit_ratio, 1.0), COALESCE(ram_overcommit_ratio, 1.0),
	COALESCE(total_cpu, 0), COALESCE(total_ram_mb, 0), COALESCE(region, '')`

func scanNode(scan func(dest ...interface{}) error) (*models.Node, error) {
	var n models.Node
	var privateIPsJSON []byte
	err := scan(&n.ID, &n.Name, &n.Address, &n.Token, &n.Status, &n.IsLocal, &n.Tags,
		&n.LinkEnabled, &n.LinkInstances, &n.LinkSecret, &n.CpusetCpus, &n.CreatedAt, &n.PublicIP, &privateIPsJSON, &n.LastSeenAt,
		&n.CPUOvercommitRatio, &n.RAMOvercommitRatio, &n.TotalCPU, &n.TotalRAMMB, &n.Region)
	if err != nil {
		return nil, err
	}
	if len(privateIPsJSON) > 0 {
		json.Unmarshal(privateIPsJSON, &n.PrivateIPs)
	}
	if n.PrivateIPs == nil {
		n.PrivateIPs = []string{}
	}
	return &n, nil
}

func (s *PostgresStore) GetNodeByID(id int) (*models.Node, error) {
	query := `SELECT ` + nodeSelectCols + ` FROM nodes WHERE id = $1`
	return scanNode(s.db.QueryRow(query, id).Scan)
}

func (s *PostgresStore) GetNodeByToken(token string) (*models.Node, error) {
	query := `SELECT ` + nodeSelectCols + ` FROM nodes WHERE token = $1`
	return scanNode(s.db.QueryRow(query, token).Scan)
}

func (s *PostgresStore) GetNodeByName(name string) (*models.Node, error) {
	query := `SELECT ` + nodeSelectCols + ` FROM nodes WHERE name = $1`
	return scanNode(s.db.QueryRow(query, name).Scan)
}

func (s *PostgresStore) ListNodes() ([]models.Node, error) {
	query := `SELECT ` + nodeSelectCols + ` FROM nodes ORDER BY id ASC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []models.Node
	for rows.Next() {
		n, err := scanNode(rows.Scan)
		if err != nil {
			continue
		}
		nodes = append(nodes, *n)
	}
	return nodes, nil
}

func (s *PostgresStore) CreateNode(n *models.Node) error {
	query := `INSERT INTO nodes (name, address, token, status, is_local, tags, link_enabled, link_instances, link_secret, region)
	          VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`
	return s.db.QueryRow(query, n.Name, n.Address, n.Token, n.Status, n.IsLocal, n.Tags, n.LinkEnabled, n.LinkInstances, n.LinkSecret, n.Region).Scan(&n.ID)
}

func (s *PostgresStore) DeleteNode(id int) error {
	_, err := s.db.Exec("DELETE FROM nodes WHERE id = $1", id)
	return err
}

func (s *PostgresStore) SetNodeStatus(id int, status string) error {
	_, err := s.db.Exec("UPDATE nodes SET status = $1 WHERE id = $2", status, id)
	return err
}

func (s *PostgresStore) SetNodeTags(id int, tags string) error {
	_, err := s.db.Exec("UPDATE nodes SET tags = $1 WHERE id = $2", tags, id)
	return err
}

func (s *PostgresStore) SetNodeName(id int, name string) error {
	_, err := s.db.Exec("UPDATE nodes SET name = $1 WHERE id = $2", name, id)
	return err
}

func (s *PostgresStore) SetNodeAddress(id int, address string) error {
	_, err := s.db.Exec("UPDATE nodes SET address = $1 WHERE id = $2", address, id)
	return err
}

func (s *PostgresStore) SetNodeIPs(id int, publicIP string, privateIPs []string) error {
	ipsJSON, err := json.Marshal(privateIPs)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("UPDATE nodes SET public_ip = $1, private_ips = $2::jsonb WHERE id = $3", publicIP, string(ipsJSON), id)
	return err
}

func (s *PostgresStore) UpdateNodeCpusetCpus(id int, cpusetCpus string) error {
	_, err := s.db.Exec("UPDATE nodes SET cpuset_cpus = $1 WHERE id = $2", cpusetCpus, id)
	return err
}

func (s *PostgresStore) SetNodeLastSeen(id int) error {
	_, err := s.db.Exec("UPDATE nodes SET last_seen_at = CURRENT_TIMESTAMP WHERE id = $1", id)
	return err
}

func (s *PostgresStore) CountServersByNode(nodeID int) (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM servers WHERE node_id = $1", nodeID).Scan(&count)
	return count, err
}

func (s *PostgresStore) ListServersByNode(nodeID int) ([]models.Server, error) {
	query := `SELECT s.id, s.uuid, s.name, s.status FROM servers s WHERE s.node_id = $1 ORDER BY s.id ASC`
	rows, err := s.db.Query(query, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var servers []models.Server
	for rows.Next() {
		var srv models.Server
		if err := rows.Scan(&srv.ID, &srv.UUID, &srv.Name, &srv.Status); err != nil {
			continue
		}
		servers = append(servers, srv)
	}
	return servers, nil
}

func (s *PostgresStore) DeleteServersByNode(nodeID int) error {
	_, err := s.db.Exec("DELETE FROM servers WHERE node_id = $1", nodeID)
	return err
}

func (s *PostgresStore) DeleteStaleOfflineNodes(offlineSince time.Time) (int, error) {
	result, err := s.db.Exec(`
		DELETE FROM nodes
		WHERE status = 'offline'
			AND last_seen_at IS NOT NULL
			AND last_seen_at < $1
			AND id NOT IN (SELECT DISTINCT node_id FROM servers)
	`, offlineSince)
	if err != nil {
		return 0, err
	}
	count, _ := result.RowsAffected()
	return int(count), nil
}

// ==========================================
// SERVERS
// ==========================================

func (s *PostgresStore) CreateServer(srv *models.Server) (int64, error) {
	var id int64
	query := `INSERT INTO servers (uuid, name, node_id, owner_id, game_image, port, memory, cpu_limit, start_command, status, is_fixed, active_sub_server, extra_jvm_flags, disk_limit, server_type, proxy_id, auto_move)
	          VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17) RETURNING id`

	err := s.db.QueryRow(query, srv.UUID, srv.Name, srv.NodeID, srv.OwnerID, srv.GameImage, srv.Port, srv.Memory, srv.CPULimit, srv.StartCommand, srv.Status, srv.IsFixed, srv.ActiveSubServer, srv.ExtraJvmFlags, srv.DiskLimit, srv.ServerType, srv.ProxyID, srv.AutoMove).Scan(&id)
	return id, err
}

// SetServerAutoMove flips the auto-move opt-in for a single server.
// The migration worker only ever considers servers with auto_move=true.
func (s *PostgresStore) SetServerAutoMove(id int, enabled bool) error {
	_, err := s.db.Exec("UPDATE servers SET auto_move = $1 WHERE id = $2", enabled, id)
	return err
}

// SumAllocatedByNode returns the total memory (MB) and cpu_limit (cores)
// allocated across every server on a node, regardless of running state.
// Used by the scheduler to enforce capacity * overcommit_ratio.
func (s *PostgresStore) SumAllocatedByNode(nodeID int) (totalRAMMB int64, totalCPU float64, err error) {
	err = s.db.QueryRow(
		`SELECT COALESCE(SUM(memory), 0), COALESCE(SUM(cpu_limit), 0) FROM servers WHERE node_id = $1`,
		nodeID,
	).Scan(&totalRAMMB, &totalCPU)
	return
}

// SetNodePlacement persists the admin-configured overcommit ratios for a node.
func (s *PostgresStore) SetNodePlacement(id int, cpuRatio, ramRatio float64) error {
	_, err := s.db.Exec(
		`UPDATE nodes SET cpu_overcommit_ratio = $1, ram_overcommit_ratio = $2 WHERE id = $3`,
		cpuRatio, ramRatio, id,
	)
	return err
}

// UpdateNodeCapacity caches the physical totals reported in the node's
// heartbeat so the scheduler does not need a live ping to compute available
// resources. Called from the discovery service when stats land.
func (s *PostgresStore) UpdateNodeCapacity(id int, totalCPU float64, totalRAMMB int64) error {
	_, err := s.db.Exec(
		`UPDATE nodes SET total_cpu = $1, total_ram_mb = $2 WHERE id = $3`,
		totalCPU, totalRAMMB, id,
	)
	return err
}

// SetNodeRegion persists the canonical region key reported by a node's
// DYLARIS_REGION env. Empty string clears the region (node is "anywhere").
func (s *PostgresStore) SetNodeRegion(id int, region string) error {
	_, err := s.db.Exec(`UPDATE nodes SET region = $1 WHERE id = $2`, region, id)
	return err
}

func (s *PostgresStore) ListServers(filterByUser string) ([]models.Server, error) {
	query := `
		SELECT s.id, s.uuid, s.name, n.name as node_name, u.username as owner_name, s.port, s.status, COALESCE(s.desired_state, 'stopped'), s.game_image, s.is_fixed, COALESCE(s.active_sub_server, ''), s.created_at, COALESCE(s.server_type, 'game'), s.proxy_id
		FROM servers s
		JOIN nodes n ON s.node_id = n.id
		JOIN users u ON s.owner_id = u.id
	`
	var rows *sql.Rows
	var err error

	if filterByUser != "" {
		query += " WHERE u.username = $1 ORDER BY s.id ASC"
		rows, err = s.db.Query(query, filterByUser)
	} else {
		query += " ORDER BY s.id ASC"
		rows, err = s.db.Query(query)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var servers []models.Server
	for rows.Next() {
		var srv models.Server
		if err := rows.Scan(&srv.ID, &srv.UUID, &srv.Name, &srv.NodeName, &srv.OwnerName, &srv.Port, &srv.Status, &srv.DesiredState, &srv.GameImage, &srv.IsFixed, &srv.ActiveSubServer, &srv.CreatedAt, &srv.ServerType, &srv.ProxyID); err != nil {
			continue
		}
		servers = append(servers, srv)
	}
	return servers, nil
}

func (s *PostgresStore) GetServerByID(id int) (*models.Server, error) {
	var srv models.Server
	query := `
		SELECT s.id, s.uuid, s.name, s.node_id, n.name as node_name, s.owner_id, u.username as owner_name, s.game_image, s.port, s.memory, COALESCE(s.cpu_limit, 0), COALESCE(s.start_command, ''), s.status, COALESCE(s.desired_state, 'stopped'), s.is_fixed, COALESCE(s.active_sub_server, ''), COALESCE(s.extra_jvm_flags, ''), s.created_at, COALESCE(s.installer_type, ''), COALESCE(s.minecraft_version, ''), COALESCE(s.build_number, ''), COALESCE(s.disk_limit, 0), COALESCE(s.server_type, 'game'), s.proxy_id, COALESCE(n.address, ''), COALESCE(s.host_port, 0), COALESCE(s.container_port, 25565), COALESCE(s.auto_move, FALSE)
		FROM servers s
		JOIN nodes n ON s.node_id = n.id
		JOIN users u ON s.owner_id = u.id
		WHERE s.id = $1
	`
	err := s.db.QueryRow(query, id).Scan(&srv.ID, &srv.UUID, &srv.Name, &srv.NodeID, &srv.NodeName, &srv.OwnerID, &srv.OwnerName, &srv.GameImage, &srv.Port, &srv.Memory, &srv.CPULimit, &srv.StartCommand, &srv.Status, &srv.DesiredState, &srv.IsFixed, &srv.ActiveSubServer, &srv.ExtraJvmFlags, &srv.CreatedAt, &srv.InstallerType, &srv.MinecraftVersion, &srv.BuildNumber, &srv.DiskLimit, &srv.ServerType, &srv.ProxyID, &srv.NodeAddress, &srv.HostPort, &srv.ContainerPort, &srv.AutoMove)
	if err != nil {
		return nil, err
	}
	return &srv, nil
}

func (s *PostgresStore) GetServerByUUID(uuid string) (*models.Server, error) {
	var srv models.Server
	query := `
		SELECT s.id, s.uuid, s.name, s.node_id, n.name as node_name, s.owner_id, u.username as owner_name, s.game_image, s.port, s.memory, COALESCE(s.cpu_limit, 0), COALESCE(s.start_command, ''), s.status, COALESCE(s.desired_state, 'stopped'), s.is_fixed, COALESCE(s.active_sub_server, ''), COALESCE(s.extra_jvm_flags, ''), s.created_at, COALESCE(s.installer_type, ''), COALESCE(s.minecraft_version, ''), COALESCE(s.build_number, ''), COALESCE(s.disk_limit, 0), COALESCE(s.server_type, 'game'), s.proxy_id, COALESCE(n.address, ''), COALESCE(s.host_port, 0), COALESCE(s.container_port, 25565)
		FROM servers s
		JOIN nodes n ON s.node_id = n.id
		JOIN users u ON s.owner_id = u.id
		WHERE s.uuid = $1
	`
	err := s.db.QueryRow(query, uuid).Scan(&srv.ID, &srv.UUID, &srv.Name, &srv.NodeID, &srv.NodeName, &srv.OwnerID, &srv.OwnerName, &srv.GameImage, &srv.Port, &srv.Memory, &srv.CPULimit, &srv.StartCommand, &srv.Status, &srv.DesiredState, &srv.IsFixed, &srv.ActiveSubServer, &srv.ExtraJvmFlags, &srv.CreatedAt, &srv.InstallerType, &srv.MinecraftVersion, &srv.BuildNumber, &srv.DiskLimit, &srv.ServerType, &srv.ProxyID, &srv.NodeAddress, &srv.HostPort, &srv.ContainerPort)
	if err != nil {
		return nil, err
	}
	return &srv, nil
}

func (s *PostgresStore) DeleteServer(id int) error {
	_, err := s.db.Exec("DELETE FROM servers WHERE id = $1", id)
	return err
}

func (s *PostgresStore) UpdateServerStatus(id int, status string) error {
	_, err := s.db.Exec("UPDATE servers SET status = $1 WHERE id = $2", status, id)
	return err
}

func (s *PostgresStore) UpdateServerDesiredState(id int, desiredState string) error {
	_, err := s.db.Exec("UPDATE servers SET desired_state = $1 WHERE id = $2", desiredState, id)
	return err
}

func (s *PostgresStore) UpdateServerSetup(id int, image, command, activeSubServer, extraJvmFlags, installerType, minecraftVersion, buildNumber string) error {
	_, err := s.db.Exec("UPDATE servers SET game_image = $1, start_command = $2, active_sub_server = $3, extra_jvm_flags = $4, installer_type = $5, minecraft_version = $6, build_number = $7 WHERE id = $8",
		image, command, activeSubServer, extraJvmFlags, installerType, minecraftVersion, buildNumber, id)
	return err
}

func (s *PostgresStore) UpdateServerActiveSubServer(id int, subServer string) error {
	_, err := s.db.Exec("UPDATE servers SET active_sub_server = $1 WHERE id = $2", subServer, id)
	return err
}

func (s *PostgresStore) UpdateServerName(id int, name string) error {
	_, err := s.db.Exec("UPDATE servers SET name = $1 WHERE id = $2", name, id)
	return err
}

func (s *PostgresStore) UpdateServerResources(id int, ram int, cpuLimit float64, diskLimit int64) error {
	_, err := s.db.Exec("UPDATE servers SET memory = $1, cpu_limit = $2, disk_limit = $3 WHERE id = $4", ram, cpuLimit, diskLimit, id)
	return err
}

func (s *PostgresStore) UpdateServerPorts(id int, hostPort, containerPort int) error {
	_, err := s.db.Exec("UPDATE servers SET host_port = $1, container_port = $2 WHERE id = $3", hostPort, containerPort, id)
	return err
}

func (s *PostgresStore) GetUsedHostPortsOnNode(nodeID int) ([]int, error) {
	rows, err := s.db.Query("SELECT host_port FROM servers WHERE node_id = $1 AND host_port > 0", nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ports []int
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err == nil {
			ports = append(ports, p)
		}
	}
	return ports, nil
}

func (s *PostgresStore) GetAllActiveServers() ([]models.Server, error) {
	rows, err := s.db.Query(`
		SELECT s.id, s.uuid, s.node_id, n.name as node_name, n.token as node_token, s.status, COALESCE(s.host_port, 0), COALESCE(s.container_port, 25565), COALESCE(s.memory, 1024), COALESCE(s.cpu_limit, 0), COALESCE(s.disk_limit, 0), COALESCE(s.start_command, ''), COALESCE(s.game_image, ''), COALESCE(s.active_sub_server, ''), COALESCE(s.extra_jvm_flags, '')
		FROM servers s
		JOIN nodes n ON s.node_id = n.id
		WHERE s.status != 'pending_setup'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var servers []models.Server
	for rows.Next() {
		var srv models.Server
		// NodeName is reused to carry the node token for migration purposes
		var nodeToken string
		if err := rows.Scan(&srv.ID, &srv.UUID, &srv.NodeID, &srv.NodeName, &nodeToken, &srv.Status, &srv.HostPort, &srv.ContainerPort, &srv.Memory, &srv.CPULimit, &srv.DiskLimit, &srv.StartCommand, &srv.GameImage, &srv.ActiveSubServer, &srv.ExtraJvmFlags); err != nil {
			return nil, err
		}
		// Store the node token in NodeAddress field temporarily (migration service reads it)
		srv.NodeAddress = nodeToken
		servers = append(servers, srv)
	}
	return servers, nil
}

func (s *PostgresStore) UpdateServerProxyID(id int, proxyID *int) error {
	_, err := s.db.Exec("UPDATE servers SET proxy_id = $1 WHERE id = $2", proxyID, id)
	return err
}

func (s *PostgresStore) UpdateServerOwner(id int, ownerID *int) error {
	if ownerID == nil {
		return fmt.Errorf("owner_id cannot be null (use DeleteServer to remove orphaned DB entries)")
	}
	_, err := s.db.Exec("UPDATE servers SET owner_id = $1 WHERE id = $2", *ownerID, id)
	return err
}

func (s *PostgresStore) CountServersByOwner(ownerID int) (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM servers WHERE owner_id = $1", ownerID).Scan(&count)
	return count, err
}

// ==========================================
// SERVER INVITES
// ==========================================

func (s *PostgresStore) CreateInvite(serverID, userID, invitedBy int, permissions map[string]bool) error {
	permsJSON, err := json.Marshal(permissions)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO server_invites (server_id, user_id, invited_by, permissions) VALUES ($1, $2, $3, $4::jsonb)`,
		serverID, userID, invitedBy, string(permsJSON))
	return err
}

func (s *PostgresStore) DeleteInvite(serverID, userID int) error {
	_, err := s.db.Exec("DELETE FROM server_invites WHERE server_id = $1 AND user_id = $2", serverID, userID)
	return err
}

func (s *PostgresStore) UpdateInvitePermissions(serverID, userID int, permissions map[string]bool) error {
	permsJSON, err := json.Marshal(permissions)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("UPDATE server_invites SET permissions = $1::jsonb WHERE server_id = $2 AND user_id = $3",
		string(permsJSON), serverID, userID)
	return err
}

func (s *PostgresStore) GetInvite(serverID, userID int) (*models.ServerInvite, error) {
	var inv models.ServerInvite
	var permsJSON []byte
	query := `
		SELECT si.id, si.server_id, si.user_id, u.username, COALESCE(u.email, ''),
			si.permissions, si.invited_by, inv_u.username, si.created_at
		FROM server_invites si
		JOIN users u ON si.user_id = u.id
		JOIN users inv_u ON si.invited_by = inv_u.id
		WHERE si.server_id = $1 AND si.user_id = $2
	`
	err := s.db.QueryRow(query, serverID, userID).Scan(
		&inv.ID, &inv.ServerID, &inv.UserID, &inv.Username, &inv.Email,
		&permsJSON, &inv.InvitedBy, &inv.InviterName, &inv.CreatedAt)
	if err != nil {
		return nil, err
	}
	json.Unmarshal(permsJSON, &inv.Permissions)
	return &inv, nil
}

func (s *PostgresStore) ListInvitesByServer(serverID int) ([]models.ServerInvite, error) {
	query := `
		SELECT si.id, si.server_id, si.user_id, u.username, COALESCE(u.email, ''),
			si.permissions, si.invited_by, inv_u.username, si.created_at
		FROM server_invites si
		JOIN users u ON si.user_id = u.id
		JOIN users inv_u ON si.invited_by = inv_u.id
		WHERE si.server_id = $1
		ORDER BY si.created_at ASC
	`
	rows, err := s.db.Query(query, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var invites []models.ServerInvite
	for rows.Next() {
		var inv models.ServerInvite
		var permsJSON []byte
		if err := rows.Scan(&inv.ID, &inv.ServerID, &inv.UserID, &inv.Username, &inv.Email,
			&permsJSON, &inv.InvitedBy, &inv.InviterName, &inv.CreatedAt); err != nil {
			continue
		}
		json.Unmarshal(permsJSON, &inv.Permissions)
		invites = append(invites, inv)
	}
	return invites, nil
}

func (s *PostgresStore) ListServersForUser(userID int, isAdmin bool) ([]models.Server, error) {
	serverCols := `s.id, s.uuid, s.name, n.name, u.username, s.port, s.status, COALESCE(s.desired_state, 'stopped'), s.game_image,
		s.is_fixed, COALESCE(s.active_sub_server, ''), s.created_at, s.owner_id,
		s.memory, COALESCE(s.cpu_limit, 0), s.node_id, COALESCE(s.extra_jvm_flags, ''), COALESCE(s.start_command, ''),
		COALESCE(s.installer_type, ''), COALESCE(s.minecraft_version, ''), COALESCE(s.build_number, ''),
		COALESCE(s.disk_limit, 0), COALESCE(s.server_type, 'game'), s.proxy_id,
		COALESCE(n.address, ''), COALESCE(s.host_port, 0), COALESCE(s.container_port, 25565)`

	scanServer := func(srv *models.Server, rows *sql.Rows, role string, permsJSON []byte) {
		if role != "" {
			srv.Role = role
		}
		if (role == "invited" || role == "inherited") && len(permsJSON) > 0 {
			var perms models.TabPermissions
			json.Unmarshal(permsJSON, &perms)
			srv.Permissions = &perms
		}
	}

	if isAdmin {
		query := `SELECT ` + serverCols + ` FROM servers s JOIN nodes n ON s.node_id = n.id JOIN users u ON s.owner_id = u.id ORDER BY s.id ASC`
		rows, err := s.db.Query(query)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var servers []models.Server
		for rows.Next() {
			var srv models.Server
			if err := rows.Scan(&srv.ID, &srv.UUID, &srv.Name, &srv.NodeName, &srv.OwnerName,
				&srv.Port, &srv.Status, &srv.DesiredState, &srv.GameImage, &srv.IsFixed, &srv.ActiveSubServer,
				&srv.CreatedAt, &srv.OwnerID, &srv.Memory, &srv.CPULimit, &srv.NodeID,
				&srv.ExtraJvmFlags, &srv.StartCommand, &srv.InstallerType, &srv.MinecraftVersion, &srv.BuildNumber,
				&srv.DiskLimit, &srv.ServerType, &srv.ProxyID, &srv.NodeAddress, &srv.HostPort, &srv.ContainerPort); err != nil {
				continue
			}
			srv.Role = "owner"
			if srv.OwnerID != userID {
				srv.Role = "admin"
			}
			servers = append(servers, srv)
		}
		return servers, nil
	}

	query := `
		SELECT ` + serverCols + `, 'owner' as role, NULL::jsonb as permissions
		FROM servers s JOIN nodes n ON s.node_id = n.id JOIN users u ON s.owner_id = u.id
		WHERE s.owner_id = $1
		UNION ALL
		SELECT ` + serverCols + `, 'invited' as role, si.permissions
		FROM server_invites si JOIN servers s ON si.server_id = s.id JOIN nodes n ON s.node_id = n.id JOIN users u ON s.owner_id = u.id
		WHERE si.user_id = $1
		UNION ALL
		SELECT ` + serverCols + `, 'inherited' as role, si.permissions
		FROM servers s JOIN servers proxy ON s.proxy_id = proxy.id
		JOIN server_invites si ON si.server_id = proxy.id AND si.user_id = $1
		JOIN nodes n ON s.node_id = n.id JOIN users u ON s.owner_id = u.id
		WHERE (si.permissions::jsonb->>'inherit')::boolean = true
			AND s.owner_id != $1
			AND NOT EXISTS (SELECT 1 FROM server_invites si2 WHERE si2.server_id = s.id AND si2.user_id = $1)
		ORDER BY id ASC
	`
	rows, err := s.db.Query(query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var servers []models.Server
	for rows.Next() {
		var srv models.Server
		var role string
		var permsJSON []byte
		if err := rows.Scan(&srv.ID, &srv.UUID, &srv.Name, &srv.NodeName, &srv.OwnerName,
			&srv.Port, &srv.Status, &srv.DesiredState, &srv.GameImage, &srv.IsFixed, &srv.ActiveSubServer,
			&srv.CreatedAt, &srv.OwnerID, &srv.Memory, &srv.CPULimit, &srv.NodeID,
			&srv.ExtraJvmFlags, &srv.StartCommand, &srv.InstallerType, &srv.MinecraftVersion, &srv.BuildNumber,
			&srv.DiskLimit, &srv.ServerType, &srv.ProxyID, &srv.NodeAddress, &srv.HostPort, &srv.ContainerPort,
			&role, &permsJSON); err != nil {
			continue
		}
		scanServer(&srv, nil, role, permsJSON)
		servers = append(servers, srv)
	}
	return servers, nil
}

// ==========================================
// MODULES
// ==========================================

const moduleSelectCols = `id, name, type, icon, COALESCE(url, ''), is_enabled, is_system, position, COALESCE(access_role, 'all')`

func (s *PostgresStore) ListModules() ([]models.Module, error) {
	query := `SELECT ` + moduleSelectCols + ` FROM modules ORDER BY position ASC, id ASC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var modules []models.Module
	for rows.Next() {
		var m models.Module
		if err := rows.Scan(&m.ID, &m.Name, &m.Type, &m.Icon, &m.URL, &m.IsEnabled, &m.IsSystem, &m.Position, &m.AccessRole); err != nil {
			continue
		}
		modules = append(modules, m)
	}
	return modules, nil
}

func (s *PostgresStore) GetModuleByID(id int) (*models.Module, error) {
	var m models.Module
	err := s.db.QueryRow(
		`SELECT `+moduleSelectCols+` FROM modules WHERE id = $1`, id,
	).Scan(&m.ID, &m.Name, &m.Type, &m.Icon, &m.URL, &m.IsEnabled, &m.IsSystem, &m.Position, &m.AccessRole)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// SetModuleAccessRole gates a module to "admin" or "all".
// Servers is hard-protected — it must stay "all".
func (s *PostgresStore) SetModuleAccessRole(id int, role string) error {
	if role != "all" && role != "admin" {
		role = "all"
	}
	_, err := s.db.Exec(`UPDATE modules SET access_role = $1 WHERE id = $2 AND name <> 'Servers'`, role, id)
	return err
}

func (s *PostgresStore) CreateModule(m *models.Module) (int, error) {
	var id int
	query := `INSERT INTO modules (name, type, icon, url, is_enabled, is_system, position) 
	          VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`
	err := s.db.QueryRow(query, m.Name, m.Type, m.Icon, m.URL, m.IsEnabled, m.IsSystem, m.Position).Scan(&id)
	return id, err
}

func (s *PostgresStore) DeleteModule(id int) error {
	_, err := s.db.Exec("DELETE FROM modules WHERE id = $1", id)
	return err
}

func (s *PostgresStore) UpdateModuleStatus(id int, isEnabled bool) error {
	_, err := s.db.Exec("UPDATE modules SET is_enabled = $1 WHERE id = $2", isEnabled, id)
	return err
}

func (s *PostgresStore) UpdateModulePosition(id int, position int) error {
	_, err := s.db.Exec("UPDATE modules SET position = $1 WHERE id = $2", position, id)
	return err
}

// ==========================================
// SETTINGS
// ==========================================

func (s *PostgresStore) GetSetting(key string) (string, error) {
	var value string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key = $1", key).Scan(&value)
	if err != nil {
		return "", err
	}
	return value, nil
}

func (s *PostgresStore) SetSetting(key, value string) error {
	_, err := s.db.Exec(`
		INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
	`, key, value)
	return err
}

// ==========================================
// STATS
// ==========================================

func (s *PostgresStore) InsertStatsBatch(stats []models.ServerStatRow) error {
	if len(stats) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO server_stats (time, server_uuid, cpu, cpu_limit, mem_used, mem_limit, players, max_players)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range stats {
		_, err := stmt.Exec(r.Time, r.ServerUUID, r.CPU, r.CPULimit, r.MemUsed, r.MemLimit, r.Players, r.MaxPlayers)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) GetStatsHistory(serverUUID string, since time.Time) ([]models.ServerStatRow, error) {
	rows, err := s.db.Query(`SELECT time, server_uuid, cpu, cpu_limit, mem_used, mem_limit, players, max_players
		FROM server_stats WHERE server_uuid = $1 AND time >= $2 ORDER BY time ASC`, serverUUID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []models.ServerStatRow
	for rows.Next() {
		var r models.ServerStatRow
		if err := rows.Scan(&r.Time, &r.ServerUUID, &r.CPU, &r.CPULimit, &r.MemUsed, &r.MemLimit, &r.Players, &r.MaxPlayers); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// ==========================================
// GATEWAY ROUTE LIMITS (still managed by Core, not Hub)
// ==========================================

func (s *PostgresStore) GetGatewayRouteLimit(scope string) (*models.GatewayRouteLimit, error) {
	var l models.GatewayRouteLimit
	err := s.db.QueryRow(`SELECT id, scope, max_routes FROM gateway_route_limits WHERE scope = $1`, scope).
		Scan(&l.ID, &l.Scope, &l.MaxRoutes)
	if err != nil {
		return nil, err
	}
	return &l, nil
}

func (s *PostgresStore) SetGatewayRouteLimit(scope string, max int) error {
	_, err := s.db.Exec(`
		INSERT INTO gateway_route_limits (scope, max_routes) VALUES ($1, $2)
		ON CONFLICT (scope) DO UPDATE SET max_routes = EXCLUDED.max_routes
	`, scope, max)
	return err
}

func (s *PostgresStore) ListGatewayRouteLimits() ([]models.GatewayRouteLimit, error) {
	rows, err := s.db.Query(`SELECT id, scope, max_routes FROM gateway_route_limits ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var limits []models.GatewayRouteLimit
	for rows.Next() {
		var l models.GatewayRouteLimit
		if err := rows.Scan(&l.ID, &l.Scope, &l.MaxRoutes); err != nil {
			continue
		}
		limits = append(limits, l)
	}
	return limits, nil
}

func (s *PostgresStore) DeleteGatewayRouteLimit(scope string) error {
	_, err := s.db.Exec("DELETE FROM gateway_route_limits WHERE scope = $1", scope)
	return err
}

// ==========================================
// LIBRARY DISABLED PATHS
// ==========================================

func (s *PostgresStore) ListDisabledLibraryPaths() ([]string, error) {
	rows, err := s.db.Query(`SELECT path FROM library_disabled`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			continue
		}
		paths = append(paths, p)
	}
	return paths, nil
}

// SetLibraryPathDisabled inserts the path when disabled=true and removes it
// when disabled=false. Idempotent in either direction.
func (s *PostgresStore) SetLibraryPathDisabled(path string, disabled bool) error {
	if disabled {
		_, err := s.db.Exec(
			`INSERT INTO library_disabled (path) VALUES ($1) ON CONFLICT (path) DO NOTHING`,
			path,
		)
		return err
	}
	_, err := s.db.Exec(`DELETE FROM library_disabled WHERE path = $1`, path)
	return err
}

// ==========================================
// SFTP
// ==========================================

// GetSFTPAccessByNode returns all (serverUUID, serverName, username) pairs for a node —
// owners and invited users combined.
func (s *PostgresStore) GetSFTPAccessByNode(nodeID int) ([]SFTPAccess, error) {
	query := `
		SELECT s.uuid, s.name, u.username
		FROM servers s
		JOIN users u ON s.owner_id = u.id
		WHERE s.node_id = $1
		UNION
		SELECT s.uuid, s.name, u.username
		FROM servers s
		JOIN server_invites si ON si.server_id = s.id
		JOIN users u ON si.user_id = u.id
		WHERE s.node_id = $1
		ORDER BY 1, 3
	`
	rows, err := s.db.Query(query, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []SFTPAccess
	for rows.Next() {
		var a SFTPAccess
		if err := rows.Scan(&a.ServerUUID, &a.ServerName, &a.Username); err != nil {
			continue
		}
		result = append(result, a)
	}
	return result, nil
}

