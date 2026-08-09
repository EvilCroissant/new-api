package model

import (
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const (
	affiliateCodeLength   = 8
	affiliateCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	affiliateCodeAttempts = 32
)

func isCurrentAffiliateCode(code string) bool {
	if len(code) != affiliateCodeLength {
		return false
	}
	return strings.IndexFunc(code, func(r rune) bool {
		return !strings.ContainsRune(affiliateCodeAlphabet, r)
	}) == -1
}

func generateAffiliateCode(tx *gorm.DB) (string, error) {
	if tx == nil {
		return "", errors.New("affiliate code transaction is nil")
	}
	for range affiliateCodeAttempts {
		bytes := make([]byte, affiliateCodeLength)
		if _, err := cryptorand.Read(bytes); err != nil {
			return "", fmt.Errorf("read random affiliate code bytes: %w", err)
		}
		var builder strings.Builder
		builder.Grow(affiliateCodeLength)
		for _, b := range bytes {
			builder.WriteByte(affiliateCodeAlphabet[int(b)%len(affiliateCodeAlphabet)])
		}
		code := builder.String()
		var count int64
		if err := tx.Model(&User{}).Where("aff_code = ?", code).Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			return code, nil
		}
	}
	return "", errors.New("failed to generate unique affiliate code")
}

func EnsureAffiliateCode(userId int) (string, error) {
	var code string
	err := DB.Transaction(func(tx *gorm.DB) error {
		user := &User{}
		if err := lockForUpdate(tx).First(user, "id = ?", userId).Error; err != nil {
			return err
		}
		if isCurrentAffiliateCode(user.AffCode) {
			code = user.AffCode
			return nil
		}
		generated, err := generateAffiliateCode(tx)
		if err != nil {
			return err
		}
		if err := tx.Model(user).Update("aff_code", generated).Error; err != nil {
			return err
		}
		code = generated
		return nil
	})
	return code, err
}

// MigrateAffiliateCodes replaces pre-referral codes with the current readable
// eight-character format. The migration is idempotent: codes already in the
// required alphabet and length are left untouched.
func MigrateAffiliateCodes() error {
	return DB.Transaction(func(tx *gorm.DB) error {
		users := make([]User, 0, 500)
		return tx.Select("id", "aff_code").FindInBatches(&users, 500, func(_ *gorm.DB, _ int) error {
			for _, user := range users {
				if isCurrentAffiliateCode(user.AffCode) {
					continue
				}
				code, err := generateAffiliateCode(tx)
				if err != nil {
					return err
				}
				if err := tx.Model(&User{}).Where("id = ?", user.Id).Update("aff_code", code).Error; err != nil {
					return err
				}
			}
			return nil
		}).Error
	})
}
