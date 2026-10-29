// Package srv 是自建测速服务端与它的客户端。同一个二进制用 --server 启动服务端。
//
// 鉴权：令牌从不上线传输。服务端发无状态随机数 nonce（自带 HMAC 签名与时间戳），
// 客户端回 proof = HMAC(token, nonce)；服务端同样无状态地验证。
// 注意：没有 TLS，流量是明文——测速用，别在这个端口上跑敏感内容。
package srv

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const Version = "0.1.0"

const nonceTTL = time.Hour

func mac(token, msg string) string {
	h := hmac.New(sha256.New, []byte(token))
	h.Write([]byte(msg))
	return hex.EncodeToString(h.Sum(nil))
}

// NewNonce 生成无状态 nonce：<ts>.<rand>.<sig>。
func NewNonce(token string, now time.Time) string {
	r := make([]byte, 8)
	_, _ = rand.Read(r)
	body := strconv.FormatInt(now.Unix(), 16) + "." + hex.EncodeToString(r)
	return body + "." + mac(token, "n|"+body)[:32]
}

func verifyNonce(token, nonce string, now time.Time) bool {
	i := strings.LastIndexByte(nonce, '.')
	if i < 0 {
		return false
	}
	body, sig := nonce[:i], nonce[i+1:]
	if !hmac.Equal([]byte(sig), []byte(mac(token, "n|"+body)[:32])) {
		return false
	}
	ts, err := strconv.ParseInt(strings.SplitN(body, ".", 2)[0], 16, 64)
	if err != nil {
		return false
	}
	age := now.Sub(time.Unix(ts, 0))
	return age > -time.Minute && age < nonceTTL
}

// Proof 是客户端对 nonce 的应答。
func Proof(token, nonce string) string { return mac(token, "p|"+nonce) }

// AuthHeader 构造 Authorization 头的值。
func AuthHeader(token, nonce string) string { return "TW " + nonce + " " + Proof(token, nonce) }

// Check 验证请求头。
func Check(token string, r *http.Request, now time.Time) bool {
	f := strings.Fields(r.Header.Get("Authorization"))
	if len(f) != 3 || f[0] != "TW" {
		return false
	}
	return verifyNonce(token, f[1], now) && hmac.Equal([]byte(f[2]), []byte(Proof(token, f[1])))
}
