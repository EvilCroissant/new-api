package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ReferralRewardSourceTopup      = "topup"
	ReferralRewardSourceRedemption = "redemption"
	ReferralRewardStatusIssued     = "issued"
	ReferralRewardStatusReversed   = "reversed"
)

type ReferralReward struct {
	Id            int    `json:"id"`
	InviterId     int    `json:"inviter_id" gorm:"index"`
	InviteeId     int    `json:"invitee_id" gorm:"index"`
	SourceType    string `json:"source_type" gorm:"type:varchar(32);uniqueIndex:idx_referral_reward_source"`
	SourceId      int    `json:"-" gorm:"uniqueIndex:idx_referral_reward_source"`
	BaseQuota     int    `json:"base_quota"`
	RewardRateBps int    `json:"reward_rate_bps"`
	RewardQuota   int    `json:"reward_quota"`
	Status        string `json:"status" gorm:"type:varchar(16);index"`
	IssuedAt      int64  `json:"issued_at" gorm:"bigint;index"`
	ReversedAt    int64  `json:"reversed_at,omitempty" gorm:"bigint"`
	Remark        string `json:"remark,omitempty" gorm:"type:varchar(255)"`
}

type ReferralRewardItem struct {
	Id            int    `json:"id"`
	SourceType    string `json:"source_type"`
	BaseQuota     int    `json:"base_quota"`
	RewardRateBps int    `json:"reward_rate_bps"`
	RewardQuota   int    `json:"reward_quota"`
	Status        string `json:"status"`
	IssuedAt      int64  `json:"issued_at"`
}

func (ReferralReward) TableName() string {
	return "referral_rewards"
}

// IssueAffiliateRewardWithTx issues one idempotent reward inside the caller's
// settlement transaction. Only successful topups and redemption codes call
// this function; manual quota changes never enter this path.
func IssueAffiliateRewardWithTx(tx *gorm.DB, inviteeId int, sourceType string, sourceId int, baseQuota int) error {
	if tx == nil {
		return errors.New("referral reward transaction is nil")
	}
	if inviteeId <= 0 || sourceId <= 0 || baseQuota <= 0 {
		return nil
	}
	if sourceType != ReferralRewardSourceTopup && sourceType != ReferralRewardSourceRedemption {
		return fmt.Errorf("unsupported referral reward source: %s", sourceType)
	}
	if !operation_setting.IsPaymentComplianceConfirmed() || common.InvitationRewardRateBps == 0 {
		return nil
	}
	if common.InvitationRewardRateBps < 0 || common.InvitationRewardRateBps > 10000 {
		return fmt.Errorf("invalid invitation reward rate: %d bps", common.InvitationRewardRateBps)
	}

	var invitee User
	if err := tx.Select("id", "inviter_id").First(&invitee, "id = ?", inviteeId).Error; err != nil {
		return err
	}
	if invitee.InviterId <= 0 || invitee.InviterId == inviteeId {
		return nil
	}

	rewardQuota := common.QuotaFromFloat(float64(baseQuota) * float64(common.InvitationRewardRateBps) / 10000)
	if rewardQuota <= 0 {
		return nil
	}
	reward := &ReferralReward{
		InviterId:     invitee.InviterId,
		InviteeId:     inviteeId,
		SourceType:    sourceType,
		SourceId:      sourceId,
		BaseQuota:     baseQuota,
		RewardRateBps: common.InvitationRewardRateBps,
		RewardQuota:   rewardQuota,
		Status:        ReferralRewardStatusIssued,
		IssuedAt:      time.Now().Unix(),
	}
	result := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "source_type"}, {Name: "source_id"}},
		DoNothing: true,
	}).Create(reward)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}

	result = tx.Model(&User{}).Where("id = ?", invitee.InviterId).Updates(map[string]interface{}{
		"aff_quota":   gorm.Expr("aff_quota + ?", rewardQuota),
		"aff_history": gorm.Expr("aff_history + ?", rewardQuota),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func GetReferralRewards(inviterId int, pageInfo *common.PageInfo) ([]*ReferralRewardItem, int64, error) {
	var rewards []*ReferralRewardItem
	query := DB.Model(&ReferralReward{}).Where("inviter_id = ?", inviterId)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := query.Select("id, source_type, base_quota, reward_rate_bps, reward_quota, status, issued_at").Order("issued_at desc, id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&rewards).Error; err != nil {
		return nil, 0, err
	}
	return rewards, total, nil
}
