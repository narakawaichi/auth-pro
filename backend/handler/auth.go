package handler

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"time"

	"auto_pro/config"
	"auto_pro/middleware"

	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type loginRequest struct {
	UserName string `json:"userName"`
	Password string `json:"password"`
	geetestValidateParams
}

// Login 管理员登录
func Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "参数错误"})
		return
	}

	if remaining := middleware.LoginLockRemaining(c.ClientIP(), req.UserName); remaining > 0 {
		c.JSON(http.StatusOK, gin.H{"code": 429, "message": fmt.Sprintf("登录尝试次数过多，请 %d 秒后重试", int(remaining.Seconds())+1)})
		return
	}

	db, err := config.DB()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "message": "数据库连接失败"})
		return
	}

	if pass, msg := verifyGeetestLogin(db, req.geetestValidateParams); !pass {
		c.JSON(http.StatusOK, gin.H{"code": 403, "message": msg})
		return
	}

	var id uint
	var passwordHash string
	var roleID sql.NullInt64
	err = db.QueryRow("SELECT id, password_hash, role_id FROM admins WHERE username = ? AND enabled = 1", req.UserName).Scan(&id, &passwordHash, &roleID)
	if err != nil {
		log.Printf("[auth] administrator lookup failed: username=%q err=%v data_dir=%q", req.UserName, err, config.GetDataDir())
		middleware.RecordLoginFailure(c.ClientIP(), req.UserName)
		c.JSON(http.StatusOK, gin.H{"code": 401, "message": "账号或密码错误"})
		return
	}

	roleCode := ""
	if roleID.Valid {
		_ = db.QueryRow("SELECT role_code FROM roles WHERE id = ?", roleID.Int64).Scan(&roleCode)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
		log.Printf("[auth] administrator password mismatch: username=%q admin_id=%d data_dir=%q", req.UserName, id, config.GetDataDir())
		middleware.RecordLoginFailure(c.ClientIP(), req.UserName)
		c.JSON(http.StatusOK, gin.H{"code": 401, "message": "账号或密码错误"})
		return
	}

	middleware.RecordLoginSuccess(c.ClientIP(), req.UserName)

	// 更新最后登录时间
	_, _ = db.Exec("UPDATE admins SET last_login_at = NOW(), last_login_ip = ? WHERE id = ?", c.ClientIP(), id)

	// 生成 token
	now := time.Now()
	claims := &middleware.Claims{
		UserID:   id,
		Username: req.UserName,
		Role:     "admin",
		RoleCode: roleCode,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(middleware.JWTSecret())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "message": "生成token失败"})
		return
	}

	// refresh token (7天)
	refreshClaims := &middleware.Claims{
		UserID:   id,
		Username: req.UserName,
		Role:     "admin",
		RoleCode: roleCode,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(7 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	refreshToken, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims).SignedString(middleware.JWTSecret())

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "登录成功",
		"data": gin.H{
			"token":        token,
			"refreshToken": refreshToken,
		},
	})
}
