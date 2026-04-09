package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"
)

// SFTPServer exposes server directories to users via SSH/SFTP.
// Auth is Redis-based: sftp:auth:{username} = bcrypt hash.
// Server list per user: sftp:node:{nodeID}:user:{username} = [{uuid,name}].
type SFTPServer struct {
	rdb        *redis.Client
	storageMgr *StorageManager
	nodeID     string
}

type sftpServerRef struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

func NewSFTPServer(rdb *redis.Client, storageMgr *StorageManager, nodeID string) *SFTPServer {
	return &SFTPServer{rdb: rdb, storageMgr: storageMgr, nodeID: nodeID}
}

func (s *SFTPServer) Start(ctx context.Context, port string) {
	hostKey, err := s.loadOrGenHostKey()
	if err != nil {
		log.Printf("SFTP: failed to load/generate host key: %v", err)
		return
	}

	config := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			return s.authUser(c.User(), string(pass))
		},
	}
	config.AddHostKey(hostKey)

	listener, err := net.Listen("tcp", ":"+strings.TrimPrefix(port, ":"))
	if err != nil {
		log.Printf("SFTP: listen error on port %s: %v", port, err)
		return
	}
	defer listener.Close()

	log.Printf("SFTP server listening on port %s", port)

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				log.Printf("SFTP: accept error: %v", err)
				continue
			}
		}
		go s.handleConn(conn, config)
	}
}

func (s *SFTPServer) authUser(username, password string) (*ssh.Permissions, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	hash, err := s.rdb.Get(ctx, "sftp:auth:"+username).Result()
	if err != nil {
		return nil, fmt.Errorf("user not found")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return nil, fmt.Errorf("invalid password")
	}
	return &ssh.Permissions{Extensions: map[string]string{"username": username}}, nil
}

func (s *SFTPServer) handleConn(conn net.Conn, config *ssh.ServerConfig) {
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer sshConn.Close()

	username := sshConn.Permissions.Extensions["username"]
	servers := s.getUserServers(username)

	go ssh.DiscardRequests(reqs)

	for ch := range chans {
		if ch.ChannelType() != "session" {
			ch.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}
		channel, requests, err := ch.Accept()
		if err != nil {
			continue
		}
		go s.handleSession(channel, requests, servers)
	}
}

func (s *SFTPServer) handleSession(channel ssh.Channel, requests <-chan *ssh.Request, servers []sftpServerRef) {
	defer channel.Close()

	for req := range requests {
		if req.Type == "subsystem" && len(req.Payload) >= 4 {
			name := string(req.Payload[4:])
			if name == "sftp" {
				if req.WantReply {
					req.Reply(true, nil)
				}
				vfs := newVirtualFS(servers, s.storageMgr)
				handlers := sftp.Handlers{
					FileGet:  vfs,
					FilePut:  vfs,
					FileCmd:  vfs,
					FileList: vfs,
				}
				sftpSrv := sftp.NewRequestServer(channel, handlers)
				sftpSrv.Serve()
				return
			}
		}
		if req.WantReply {
			req.Reply(false, nil)
		}
	}
}

func (s *SFTPServer) getUserServers(username string) []sftpServerRef {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	key := fmt.Sprintf("sftp:node:%s:user:%s", s.nodeID, username)
	data, err := s.rdb.Get(ctx, key).Result()
	if err != nil {
		return nil
	}
	var servers []sftpServerRef
	json.Unmarshal([]byte(data), &servers)
	return servers
}

func (s *SFTPServer) loadOrGenHostKey() (ssh.Signer, error) {
	keyPath := filepath.Join("data", "sftp_host_key")
	os.MkdirAll(filepath.Dir(keyPath), 0700)

	if data, err := os.ReadFile(keyPath); err == nil {
		block, _ := pem.Decode(data)
		if block != nil {
			key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
			if err == nil {
				return ssh.NewSignerFromKey(key)
			}
		}
	}

	// Generate new RSA key
	key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, err
	}
	pemData := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if err := os.WriteFile(keyPath, pemData, 0600); err != nil {
		log.Printf("SFTP: could not persist host key: %v", err)
	}
	return ssh.NewSignerFromKey(key)
}

// ==========================================
// Virtual FS
// ==========================================

// virtualFS maps root-level entries to server directories.
// /{serverName}/... → storageMgr.GetServerDir(uuid)/...
type virtualFS struct {
	servers    []sftpServerRef
	storageMgr *StorageManager
	nameToUUID map[string]string
}

func newVirtualFS(servers []sftpServerRef, sm *StorageManager) *virtualFS {
	m := make(map[string]string, len(servers))
	for _, s := range servers {
		m[s.Name] = s.UUID
	}
	return &virtualFS{servers: servers, storageMgr: sm, nameToUUID: m}
}

// resolve converts a virtual path to a real OS path.
// Returns ("", nil) for root, (realPath, nil) for valid paths, ("", os.ErrNotExist) for invalid.
func (v *virtualFS) resolve(path string) (string, error) {
	path = filepath.ToSlash(filepath.Clean("/" + path))
	if path == "/" {
		return "", nil // root — virtual
	}

	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)
	serverName := parts[0]
	uuid, ok := v.nameToUUID[serverName]
	if !ok {
		return "", os.ErrNotExist
	}

	base := v.storageMgr.GetServerDir(uuid)
	if len(parts) == 1 {
		return base, nil
	}
	return filepath.Join(base, filepath.FromSlash(parts[1])), nil
}

// --- sftp.ReadWriteAt (FileGet + FilePut) ---

func (v *virtualFS) Fileread(r *sftp.Request) (io.ReadCloser, error) {
	realPath, err := v.resolve(r.Filepath)
	if err != nil || realPath == "" {
		return nil, os.ErrPermission
	}
	return os.Open(realPath)
}

func (v *virtualFS) Filewrite(r *sftp.Request) (io.WriteCloser, error) {
	realPath, err := v.resolve(r.Filepath)
	if err != nil || realPath == "" {
		return nil, os.ErrPermission
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	return os.OpenFile(realPath, flags, 0644)
}

// --- sftp.FileCmder ---

func (v *virtualFS) Filecmd(r *sftp.Request) error {
	switch r.Method {
	case "Mkdir":
		realPath, err := v.resolve(r.Filepath)
		if err != nil || realPath == "" {
			return os.ErrPermission
		}
		return os.Mkdir(realPath, 0755)
	case "Remove":
		realPath, err := v.resolve(r.Filepath)
		if err != nil || realPath == "" {
			return os.ErrPermission
		}
		return os.Remove(realPath)
	case "Rename":
		src, err := v.resolve(r.Filepath)
		if err != nil || src == "" {
			return os.ErrPermission
		}
		dst, err := v.resolve(r.Target)
		if err != nil || dst == "" {
			return os.ErrPermission
		}
		return os.Rename(src, dst)
	case "Setstat":
		return nil // ignore chmod/chown
	}
	return fmt.Errorf("unsupported operation: %s", r.Method)
}

// --- sftp.ListerAt (FileList) ---

type listerAt []os.FileInfo

func (l listerAt) ListAt(ls []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(ls, l[offset:])
	if n < len(ls) {
		return n, io.EOF
	}
	return n, nil
}

func (v *virtualFS) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	switch r.Method {
	case "List":
		realPath, err := v.resolve(r.Filepath)
		if err != nil {
			return nil, err
		}
		if realPath == "" {
			// Root: list virtual server entries
			entries := make([]os.FileInfo, 0, len(v.servers))
			for _, s := range v.servers {
				base := v.storageMgr.GetServerDir(s.UUID)
				fi, err := os.Stat(base)
				if err != nil {
					fi = &virtualDirInfo{name: s.Name}
				} else {
					fi = &namedFileInfo{FileInfo: fi, name: s.Name}
				}
				entries = append(entries, fi)
			}
			return listerAt(entries), nil
		}
		entries, err := os.ReadDir(realPath)
		if err != nil {
			return nil, err
		}
		infos := make([]os.FileInfo, 0, len(entries))
		for _, e := range entries {
			fi, err := e.Info()
			if err == nil {
				infos = append(infos, fi)
			}
		}
		return listerAt(infos), nil

	case "Stat", "Lstat":
		realPath, err := v.resolve(r.Filepath)
		if err != nil {
			return nil, err
		}
		if realPath == "" {
			return listerAt([]os.FileInfo{&virtualDirInfo{name: "/"}}), nil
		}
		fi, err := os.Lstat(realPath)
		if err != nil {
			return nil, err
		}
		// If it's a server root (top-level), use display name
		path := filepath.ToSlash(filepath.Clean("/" + r.Filepath))
		if strings.Count(path, "/") == 1 {
			serverName := strings.TrimPrefix(path, "/")
			fi = &namedFileInfo{FileInfo: fi, name: serverName}
		}
		return listerAt([]os.FileInfo{fi}), nil
	}
	return nil, fmt.Errorf("unsupported list method: %s", r.Method)
}

// --- helper types ---

type virtualDirInfo struct{ name string }

func (d *virtualDirInfo) Name() string      { return d.name }
func (d *virtualDirInfo) Size() int64       { return 0 }
func (d *virtualDirInfo) Mode() os.FileMode { return os.ModeDir | 0755 }
func (d *virtualDirInfo) ModTime() time.Time { return time.Time{} }
func (d *virtualDirInfo) IsDir() bool       { return true }
func (d *virtualDirInfo) Sys() interface{}  { return nil }

type namedFileInfo struct {
	os.FileInfo
	name string
}

func (n *namedFileInfo) Name() string { return n.name }
