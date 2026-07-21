package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	sms "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms/v20210111"
)

type FixedSMSProvider struct {
	Code string
}

func (p FixedSMSProvider) FixedCode() string { return p.Code }

func (p FixedSMSProvider) SendCode(context.Context, string, string, time.Duration) error {
	if len(p.Code) != 6 {
		return errors.New("fixed 短信验证码必须为 6 位")
	}
	return nil
}

type TencentSMSConfig struct {
	SecretID   string
	SecretKey  string
	Region     string
	SDKAppID   string
	SignName   string
	TemplateID string
}

type TencentSMSProvider struct {
	client     *sms.Client
	sdkAppID   string
	signName   string
	templateID string
}

func NewTencentSMSProvider(cfg TencentSMSConfig) (*TencentSMSProvider, error) {
	if cfg.SecretID == "" || cfg.SecretKey == "" || cfg.Region == "" || cfg.SDKAppID == "" || cfg.SignName == "" || cfg.TemplateID == "" {
		return nil, errors.New("腾讯云短信配置不完整")
	}
	client, err := sms.NewClient(common.NewCredential(cfg.SecretID, cfg.SecretKey), cfg.Region, profile.NewClientProfile())
	if err != nil {
		return nil, err
	}
	return &TencentSMSProvider{client: client, sdkAppID: cfg.SDKAppID, signName: cfg.SignName, templateID: cfg.TemplateID}, nil
}

func (p *TencentSMSProvider) SendCode(ctx context.Context, phone, code string, ttl time.Duration) error {
	request := sms.NewSendSmsRequest()
	request.SetContext(ctx)
	request.PhoneNumberSet = []*string{common.StringPtr(phone)}
	request.SmsSdkAppId = common.StringPtr(p.sdkAppID)
	request.SignName = common.StringPtr(p.signName)
	request.TemplateId = common.StringPtr(p.templateID)
	request.TemplateParamSet = []*string{common.StringPtr(code), common.StringPtr(fmt.Sprintf("%d", int(ttl.Minutes())))}
	response, err := p.client.SendSms(request)
	if err != nil {
		return err
	}
	if response.Response == nil || len(response.Response.SendStatusSet) != 1 || response.Response.SendStatusSet[0].Code == nil || *response.Response.SendStatusSet[0].Code != "Ok" {
		return errors.New("腾讯云短信发送失败")
	}
	return nil
}
