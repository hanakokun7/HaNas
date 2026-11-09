package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

var jwtSecret string

var db *gorm.DB

var (
	fileLocks = struct {
		sync.RWMutex
		m map[uint]*sync.RWMutex
	}{m: make(map[uint]*sync.RWMutex)}
	nodeLocks = struct {
		sync.RWMutex
		m map[uint]*sync.Mutex
	}{m: make(map[uint]*sync.Mutex)}
	uploadMutex    sync.Mutex
	thumbnailMutex sync.Map
)

type Config struct {
	ID    uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	Key   string `gorm:"uniqueIndex;not null" json:"key"`
	Value string `gorm:"not null" json:"value"`
}

type User struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Username  string    `gorm:"uniqueIndex;not null" json:"username"`
	Password  string    `gorm:"not null" json:"-"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}

type Claims struct {
	UserID   uint   `json:"user_id"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

type Share struct {
	ID        uint       `gorm:"primaryKey;autoIncrement" json:"id"`
	Token     string     `gorm:"uniqueIndex;not null" json:"token"`
	NodeID    uint       `gorm:"not null;index" json:"node_id"`
	UserID    uint       `gorm:"not null;index" json:"user_id"`
	CreatedAt time.Time  `gorm:"autoCreateTime" json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type Node struct {
	ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID     uint      `gorm:"not null;index" json:"user_id"`
	Fid        *uint     `gorm:"uniqueIndex;check:((is_dir = true AND fid IS NULL) OR (is_dir = false AND fid IS NOT NULL))" json:"-"`
	Name       string    `gorm:"not null" json:"name"`
	IsDir      bool      `gorm:"not null" json:"is_dir"`
	OyaID      *uint     `gorm:"index" json:"oya_id,omitempty"`
	Ko         []Node    `gorm:"foreignKey:OyaID;references:ID;constraint:OnDelete:CASCADE" json:"ko,omitempty"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime" json:"updated_at"`
	Size       int64     `gorm:"-" json:"size,omitempty"`
	Path       string    `gorm:"-" json:"path,omitempty"`
	ShareToken string    `gorm:"-" json:"share_token,omitempty"`
	Del_F      bool      `gorm:"not null;default:false" json:"-"`
}

func (n Node) to_json() []byte {
	data, _ := json.Marshal(n)
	return data
}

func (n Node) return_file() []byte {
	if n.Fid == nil {
		return nil
	}
	p := dataDir + "/" + fmt.Sprintf("%d", *n.Fid)
	data, _ := os.ReadFile(p)
	return data
}

type Trash struct {
	Nid       uint      `gorm:"not null" json:"nid"`
	DeletedAt time.Time `gorm:"autoUpdateTime" json:"deleted_at"`
}

func (t Trash) getInfo() []byte {
	var tmp Node
	db.Preload("Ko").First(&tmp, "id = ?", t.Nid)
	return tmp.to_json()
}

func (t Trash) restore() {
	var tmp Node
	db.Preload("Ko").First(&tmp, "id = ?", t.Nid)
	tmp.Del_F = false
	r := db.Save(&tmp)
	if r.Error == nil {
		db.Delete(&t)
	}
}

func (t Trash) delete() {
	var tmp Node
	db.Preload("Ko").First(&tmp, "id = ?", t.Nid)
	if DeleteNodeRecursive(tmp.ID, tmp.UserID) == nil {
		db.Delete(&t)
	}
}
