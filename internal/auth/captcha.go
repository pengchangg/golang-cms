package auth

import (
	"image"
	"image/color"
	"image/draw"
	"time"

	"github.com/wenlng/go-captcha/v2/slide"
)

type CaptchaData struct {
	BackgroundImage string
	TileImage       string
	TileX           int
	TileY           int
	TargetX         int
	TargetY         int
}

type CaptchaGenerator interface {
	Generate() (CaptchaData, error)
}

type GoCaptchaGenerator struct {
	captcha slide.Captcha
}

func NewGoCaptchaGenerator() *GoCaptchaGenerator {
	background := image.NewNRGBA(image.Rect(0, 0, 300, 220))
	for y := 0; y < 220; y++ {
		for x := 0; x < 300; x++ {
			background.SetNRGBA(x, y, color.NRGBA{R: uint8(24 + x/5), G: uint8(70 + y/4), B: uint8(105 + (x+y)/8), A: 255})
		}
	}
	mask := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	shadow := image.NewNRGBA(mask.Bounds())
	overlay := image.NewNRGBA(mask.Bounds())
	draw.Draw(mask, image.Rect(6, 6, 58, 58), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(shadow, image.Rect(6, 6, 58, 58), &image.Uniform{C: color.NRGBA{A: 135}}, image.Point{}, draw.Src)
	draw.Draw(overlay, image.Rect(6, 6, 58, 58), &image.Uniform{C: color.NRGBA{R: 255, G: 255, B: 255, A: 90}}, image.Point{}, draw.Src)
	builder := slide.NewBuilder()
	builder.SetResources(slide.WithBackgrounds([]image.Image{background}), slide.WithGraphImages([]*slide.GraphImage{{OverlayImage: overlay, ShadowImage: shadow, MaskImage: mask}}))
	return &GoCaptchaGenerator{captcha: builder.Make()}
}

func (g *GoCaptchaGenerator) Generate() (CaptchaData, error) {
	data, err := g.captcha.Generate()
	if err != nil {
		return CaptchaData{}, err
	}
	background, err := data.GetMasterImage().ToBase64()
	if err != nil {
		return CaptchaData{}, err
	}
	tile, err := data.GetTileImage().ToBase64()
	if err != nil {
		return CaptchaData{}, err
	}
	block := data.GetData()
	return CaptchaData{BackgroundImage: background, TileImage: tile, TileX: block.DX, TileY: block.DY, TargetX: block.X, TargetY: block.Y}, nil
}

type CaptchaResponse struct {
	ChallengeID     string    `json:"challenge_id"`
	BackgroundImage string    `json:"background_image"`
	TileImage       string    `json:"tile_image"`
	TileX           int       `json:"tile_x"`
	TileY           int       `json:"tile_y"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type SMSChallengeResponse struct {
	ChallengeID       string    `json:"challenge_id"`
	PhoneMasked       string    `json:"phone_masked"`
	ExpiresAt         time.Time `json:"expires_at"`
	RetryAfterSeconds int       `json:"retry_after_seconds"`
}
