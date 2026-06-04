# go-webp

go-webpはGo標準の `image.Image` インターフェイス向けのpure Go WebPエンコーダです

デフォルトではVP8L lossless WebPを書き出し、VP8ベースのlossy WebPも書き出せます
APIはGo標準の画像パッケージに近く、`io.Writer`、`image.Image`、任意のencoder optionsを受け取ります

## インストール

```sh
go get github.com/mayahiro/go-webp
```

## 使い方

```go
package main

import (
	"image"
	"image/color"
	"os"

	"github.com/mayahiro/go-webp"
)

func main() {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	img.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 255})
	img.SetNRGBA(0, 1, color.NRGBA{B: 255, A: 255})
	img.SetNRGBA(1, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 255})

	f, err := os.Create("out.webp")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	if err := webp.Encode(f, img, nil); err != nil {
		panic(err)
	}
}
```

## API

```go
func Encode(w io.Writer, m image.Image, o *Options) error
```

`Encode` はlossless WebP画像を `w` に書き込みます
`Options` がnilの場合はデフォルトのlossless設定を使います

lossy WebPを書き出す場合は `CompressionLossy` を指定します

```go
err := webp.Encode(w, img, &webp.Options{
	Compression: webp.CompressionLossy,
	Quality:     80,
})
```

`Quality` はlossy品質を1から100で制御します
0以下はデフォルト、100を超える値は100に丸められ、lossless encodingでは無視されます

```go
type Encoder struct {
	Options *Options
}

func (enc *Encoder) Encode(w io.Writer, m image.Image) error
```

`Encoder` は `image/png.Encoder` などに近い形で、将来のoptions追加に備えています

## 性能メモ

- pure Goで実装しておりcgoは使いません
- lossless encodingでは入力画像を2回走査し、変換済み画像全体は保持しません
- 単一値のチャンネルはsingle-symbol Huffman treeでエンコードします
- 現在はVP8L transforms、color cache、LZ77 backwards referencesを使わないため、高度に最適化されたWebP encoderより出力が大きくなることがあります
- lossy encodingは4:2:0 chroma subsampling、選択されたintra16x16/luma4x4/chroma prediction mode、量子化されたDC/AC係数を使う低複雑度VP8 key frame encoderです。qualityに応じたlevelでsimple VP8 loop filterを有効化します
- lossy `Quality` は現時点ではVP8 base quantizerを制御し、mode decisionは単純なrate-distortion heuristicです
- alpha付きのlossy画像はextended WebPとして書き出し、`ALPH` チャンクで透明度を保持します。圧縮したほうが小さい場合はcompressed alphaを使い、それ以外はraw alphaに戻します

## 制限

- エンコードのみ対応しています
- lossless画像サイズは各軸1から16384 pixelsの範囲が必要です
- lossy画像サイズは各軸1から16383 pixelsの範囲が必要です
- `image.NRGBA` 以外の画像は `color.NRGBAModel` を通して変換してからエンコードします
- lossy alpha圧縮は意図的に単純な実装で、現時点ではLZ77 referencesを使わず、頻度ベースのalpha residual符号化を使います
- lossy normal loop filteringはまだ未実装のため、細部の多い画像では高度に最適化されたVP8/WebP encoderよりブロック感が強い、または大きくなることがあります

## 対応環境

- Go 1.25.0以上

## 確認

```sh
go test ./...
go vet ./...
go tool goimports -w .
```

## ライセンス

MIT
