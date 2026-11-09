package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"io"
	mrand "math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func hashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), 14)
	return string(bytes), err
}

func checkPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

func initJWTSecret() error {
	var config Config
	if err := db.First(&config, "key = ?", "jwt_secret").Error; err != nil {
		b := make([]byte, 32)
		for i := range b {
			b[i] = byte(mrand.Intn(256))
		}
		jwtSecret = base64.StdEncoding.EncodeToString(b)
		config = Config{
			Key:   "jwt_secret",
			Value: jwtSecret,
		}
		if err := db.Create(&config).Error; err != nil {
			return fmt.Errorf("failed to save jwt secret: %w", err)
		}
		fmt.Println("Generated new JWT secret")
	} else {
		jwtSecret = config.Value
		fmt.Println("Loaded JWT secret from database")
	}
	return nil
}

func generateToken(userID uint, username string) (string, error) {
	claims := &Claims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(876000 * time.Hour)), // ~100 years
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(jwtSecret))
}

func search(t string, u uint) []Node {
	var tmp []Node
	db.Where("Username = ? AND Name LIKE ?", u, "%"+t+"%").Find(&tmp)
	return tmp
}

func UploadFile(reader io.Reader) (uint, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return 0, fmt.Errorf("cannot create data dir: %w", err)
	}
	uploadMutex.Lock()
	r := mrand.New(mrand.NewSource(time.Now().UnixNano()))
	var filename uint
	for {
		filename = uint(r.Intn(100000000))
		filepath := fmt.Sprintf("%s/%d", dataDir, filename)
		file, err := os.OpenFile(filepath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			uploadMutex.Unlock()
			return 0, fmt.Errorf("cannot create file: %w", err)
		}
		uploadMutex.Unlock()
		_, err = io.Copy(file, reader)
		file.Close()
		if err != nil {
			os.Remove(filepath)
			return 0, fmt.Errorf("cannot write file: %w", err)
		}
		break
	}
	return filename, nil
}

func UploadNode(filename string, reader io.Reader, isDir bool, oyaID *uint, userID uint) (uint, error) {
	var nodeID uint
	err := db.Transaction(func(tx *gorm.DB) error {
		var existing Node
		if err := tx.First(&existing, "name = ? AND oya_id = ? AND user_id = ?", filename, oyaID, userID).Error; err == nil {
			if isDir {
				if existing.IsDir {
					nodeID = existing.ID
					return nil
				}
				if err := DeleteNodeRecursive(existing.ID, userID); err != nil {
					return err
				}
				newNode := Node{
					UserID: userID,
					Name:   filename,
					IsDir:  true,
					OyaID:  oyaID,
				}
				if res := tx.Create(&newNode); res.Error != nil {
					return res.Error
				}
				nodeID = newNode.ID
				return nil
			}
			if existing.IsDir {
				return fmt.Errorf("folder_exists")
			}
			if !UpdateNode(existing.Fid, reader) {
				return fmt.Errorf("failed to update existing node")
			}
			existing.UpdatedAt = time.Now()
			if err := tx.Save(&existing).Error; err != nil {
				return fmt.Errorf("failed to update timestamp: %w", err)
			}
			nodeID = existing.ID
			return nil
		}
		if isDir {
			newNode := Node{
				UserID: userID,
				Name:   filename,
				IsDir:  true,
				OyaID:  oyaID,
			}
			if result := tx.Create(&newNode); result.Error != nil {
				return result.Error
			}
			nodeID = newNode.ID
			return nil
		}
		fid, err := UploadFile(reader)
		if err != nil {
			return err
		}
		newNode := Node{
			UserID: userID,
			Fid:    &fid,
			Name:   filename,
			IsDir:  false,
			OyaID:  oyaID,
		}
		if result := tx.Create(&newNode); result.Error != nil {
			return result.Error
		}
		nodeID = newNode.ID
		return nil
	})
	return nodeID, err
}

func UpdateNode(fid *uint, reader io.Reader) bool {
	if fid == nil {
		return false
	}
	fileLocks.Lock()
	lock, exists := fileLocks.m[*fid]
	if !exists {
		lock = &sync.RWMutex{}
		fileLocks.m[*fid] = lock
	}
	fileLocks.Unlock()
	lock.Lock()
	defer lock.Unlock()
	filepath := fmt.Sprintf("%s/%d", dataDir, *fid)
	tmpPath := filepath + ".tmp"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return false
	}
	_, err = io.Copy(file, reader)
	file.Close()
	if err != nil {
		os.Remove(tmpPath)
		return false
	}
	if err := os.Rename(tmpPath, filepath); err != nil {
		os.Remove(tmpPath)
		return false
	}
	return true
}

func CopyNode(src Node, newOyaID uint, userID uint) (uint, error) {
	if src.IsDir {
		newNode := Node{
			UserID: userID,
			Name:   src.Name,
			IsDir:  true,
			OyaID:  &newOyaID,
		}
		if res := db.Create(&newNode); res.Error != nil {
			return 0, res.Error
		}
		var children []Node
		db.Where("oya_id = ? AND user_id = ?", src.ID, userID).Find(&children)
		for _, c := range children {
			_, err := CopyNode(c, newNode.ID, userID)
			if err != nil {
				fmt.Println("warning: copy child failed:", err)
			}
		}
		return newNode.ID, nil
	}
	fid, err := UploadFile(bytes.NewReader(src.return_file()))
	if err != nil {
		return 0, err
	}
	newNode := Node{
		UserID: userID,
		Fid:    &fid,
		Name:   src.Name,
		IsDir:  false,
		OyaID:  &newOyaID,
	}
	result := db.Create(&newNode)
	if result.Error != nil {
		return 0, result.Error
	}
	return newNode.ID, nil
}

func findChildByName(oyaID uint, name string, userID uint) (Node, bool) {
	var n Node
	if err := db.First(&n, "oya_id = ? AND name = ? AND user_id = ?", oyaID, name, userID).Error; err != nil {
		return Node{}, false
	}
	return n, true
}

func isAncestor(ancestorID uint, nodeID uint, userID uint) bool {
	var cur Node
	if err := db.First(&cur, "id = ? AND user_id = ?", nodeID, userID).Error; err != nil {
		return false
	}
	for cur.OyaID != nil {
		if *cur.OyaID == ancestorID {
			return true
		}
		if err := db.First(&cur, "id = ? AND user_id = ?", *cur.OyaID, userID).Error; err != nil {
			break
		}
	}
	return false
}

func DeleteNodeRecursive(id uint, userID uint) error {
	var n Node
	if err := db.Preload("Ko").First(&n, "id = ? AND user_id = ?", id, userID).Error; err != nil {
		return err
	}
	for _, c := range n.Ko {
		_ = DeleteNodeRecursive(c.ID, userID)
	}
	if n.Fid != nil {
		p := fmt.Sprintf("%s/%d", dataDir, *n.Fid)
		_ = os.Remove(p)
		thumbPath := fmt.Sprintf("%s/%d.jpg", thumbDir, *n.Fid)
		_ = os.Remove(thumbPath)
	}
	result := db.Delete(&n)
	if result.Error != nil {
		return result.Error
	}
	return nil
}

func MoveNode(src Node, newOyaID uint) error {
	src.OyaID = &newOyaID
	result := db.Save(&src)
	return result.Error
}

func RenameNode(src Node, newName string) error {
	src.Name = newName
	result := db.Save(&src)
	return result.Error
}

func DeleteNode(src Node) error {
	result := db.Delete(&src)
	success := result.Error == nil && result.RowsAffected > 0
	if !success {
		return fmt.Errorf("delete failed")
	}
	fid := src.Fid
	if fid != nil {
		p := dataDir + "/" + fmt.Sprintf("%d", *fid)
		_ = os.Remove(p)
	}
	return nil
}

func calculateDirSize(nodeID uint, userID uint) int64 {
	var totalSize int64
	var children []Node
	db.Where("oya_id = ? AND user_id = ?", nodeID, userID).Find(&children)
	for _, child := range children {
		if child.IsDir {
			totalSize += calculateDirSize(child.ID, userID)
		} else if child.Fid != nil {
			p := fmt.Sprintf("%s/%d", dataDir, *child.Fid)
			if st, err := os.Stat(p); err == nil {
				totalSize += st.Size()
			}
		}
	}
	return totalSize
}

func extractVideoFrame(videoPath string) (image.Image, error) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found: %v", err)
	}
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("thumb_%d.jpg", time.Now().UnixNano()))
	defer os.Remove(tmpFile)
	cmd := exec.Command(ffmpegPath,
		"-i", videoPath,
		"-ss", "00:00:05",
		"-vframes", "1",
		"-vf", "scale=200:200:force_original_aspect_ratio=decrease,pad=200:200:-1:-1:color=black",
		"-q:v", "2",
		"-f", "image2",
		"-update", "1",
		"-y",
		tmpFile,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg error: %v - stderr: %s", err, stderr.String())
	}
	file, err := os.Open(tmpFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open thumbnail: %v", err)
	}
	defer file.Close()
	img, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("failed to decode thumbnail: %v", err)
	}
	return img, nil
}

var progressChannels = struct {
	sync.RWMutex
	m map[string]chan int
}{m: make(map[string]chan int)}

type ProgressReader struct {
	r        io.Reader
	total    int64
	read     int64
	lastPct  int
	uploadID string
}

func (pr *ProgressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	if n > 0 {
		pr.read += int64(n)
		if pr.total > 0 && pr.uploadID != "" {
			pct := int(float64(pr.read) * 100 / float64(pr.total))
			if pct > pr.lastPct && pct < 100 {
				pr.lastPct = pct
				progressChannels.RLock()
				ch, ok := progressChannels.m[pr.uploadID]
				progressChannels.RUnlock()
				if ok {
					select {
					case ch <- pct:
					default:
					}
				}
			}
		}
	}
	return n, err
}

func generateShareToken() string {
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(mrand.Intn(256))
	}
	return base64.URLEncoding.EncodeToString(b)
}
