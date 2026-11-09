package main

import (
	_ "embed"
	"fmt"
	_ "image/gif"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

const (
	dataDir     = "./data"
	thumbDir    = "./thumbnails"
	dbFile      = "./database.db"
	programName = "HaNas"
)

//go:embed index.html
var indexHtmlContent string

//go:embed assets/index.js
var js_script string

//go:embed assets/index.css
var css_style string

//go:embed assets/icon.ico
var favicon []byte

//go:embed assets/apple-touch-icon.png
var appleTouchIcon []byte

func main() {
	var err error
	db, err = gorm.Open(sqlite.Open(dbFile), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	db.AutoMigrate(&Config{}, &User{}, &Node{}, &Share{}, &Trash{})
	if err := initJWTSecret(); err != nil {
		panic(err)
	}
	go gomikago_akeru()
	http.HandleFunc("/register", Register)
	http.HandleFunc("/login", Login)
	http.HandleFunc("/logout", Logout)
	http.HandleFunc("/me", authMiddleware(Me))
	http.HandleFunc("/file/", authMiddleware(GetFile))
	http.HandleFunc("/thumbnail/", authMiddleware(GetThumbnail))
	http.HandleFunc("/node/", authMiddleware(GetJson))
	http.HandleFunc("/upload", authMiddleware(UpFile))
	http.HandleFunc("/upload/progress", UploadProgressSSE)
	http.HandleFunc("/copy", authMiddleware(CpFile))
	http.HandleFunc("/move", authMiddleware(MvFile))
	http.HandleFunc("/rename", authMiddleware(RnFile))
	http.HandleFunc("/delete", authMiddleware(DlFile))
	http.HandleFunc("/share/create", authMiddleware(CreateShare))
	http.HandleFunc("/share/delete", authMiddleware(DeleteShare))
	http.HandleFunc("/s/", GetSharedFile)
	http.HandleFunc("/delete-account", authMiddleware(DeleteAccount))
	http.HandleFunc("/search", authMiddleware(search))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(indexHtmlContent))
	})
	http.HandleFunc("/index.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Write([]byte(js_script))
	})
	http.HandleFunc("/index.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Write([]byte(css_style))
	})
	http.HandleFunc("/icon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/x-icon")
		w.Write(favicon)
	})
	http.HandleFunc("/apple-touch-icon.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(appleTouchIcon)
	})
	http.HandleFunc("/config.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		exePath, err := os.Executable()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		dir := filepath.Dir(exePath)
		configPath := filepath.Join(dir, "config.js")
		data, err := os.ReadFile(configPath)
		if err != nil {
			http.Error(w, "config.js not found", http.StatusNotFound)
			return
		}
		w.Write(data)
	})
	fmt.Printf("%s server started at :80\n", programName)
	http.ListenAndServe(":80", nil)
}
