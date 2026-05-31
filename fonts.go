package main

import (
	_ "embed"
	"encoding/base64"
)

//go:embed assets/fonts/NotoSans-Regular.ttf
var notoSansRegularTTF []byte

//go:embed assets/fonts/NotoSans-Bold.ttf
var notoSansBoldTTF []byte

func embeddedFontCSS() string {
	return `@font-face{font-family:"Noto Sans";font-style:normal;font-weight:400;font-display:swap;src:url("data:font/ttf;base64,` +
		base64.StdEncoding.EncodeToString(notoSansRegularTTF) +
		`") format("truetype")}@font-face{font-family:"Noto Sans";font-style:normal;font-weight:700;font-display:swap;src:url("data:font/ttf;base64,` +
		base64.StdEncoding.EncodeToString(notoSansBoldTTF) +
		`") format("truetype")}`
}
