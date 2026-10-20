package main

import (
	"fmt"

	"github.com/golang-jwt/jwt/v4"
)

func main() {
	rawJWT := `eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJVc2VySUQiOiI4MjkzODEzMTgzIiwiUGxhdGZvcm1JRCI6NSwiZXhwIjoxNzA2NTk0MTU0LCJuYmYiOjE2OTg4MTc4NTQsImlhdCI6MTY5ODgxODE1NH0.QCJHzU07SC6iYBoFO6Zsm61TNDor2D89I4E3zg8HHHU`

	// 校验 token
	claims := &jwt.MapClaims{}
	parsedT, err := jwt.ParseWithClaims(rawJWT, claims, func(token *jwt.Token) (any, error) {
		// 校验签名算法是否为 HMAC
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		if kid, ok := token.Header["kid"].(string); ok {
			fmt.Println("kid", kid)
		}

		return []byte("key1"), nil
	})

	if err != nil || !parsedT.Valid {
		fmt.Println("token valid failed", err)

		return
	}

	fmt.Println("ok")
}
