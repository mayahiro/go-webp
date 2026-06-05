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
- 現在のローカルbenchmark参考値は [BENCHMARKS.md](BENCHMARKS.md) に記載しています
- lossless encodingでは入力画像を2回走査し、変換済み画像全体は保持しません
- 単一値のチャンネルはsingle-symbol Huffman treeでエンコードします
- lossless encoderはVP8L transforms、color cache、LZ77 backwards referencesを使わないため、高度に最適化されたWebP encoderよりlossless出力が大きくなることがあります
- lossy encodingは4:2:0 chroma subsampling、adaptive chroma downsampling、選択されたintra16x16/luma4x4/chroma prediction mode、量子化されたDC/AC係数を使う低複雑度VP8 key frame encoderです
- 出力サイズ削減が見込める場合はresidual token probability updateを書き込み、qualityに応じたsharpnessとluma4x4 macroblock向けmode deltaを持つnormal VP8 loop filterを有効化します
- lossy `Quality` は現時点では非線形mappingでVP8 base quantizerを制御し、quality依存のY2/UV quantizationとloop filter設定を使います
- mode decisionは単純なrate-distortion heuristicです
- alpha付きのlossy画像はextended WebPとして書き出し、`ALPH` チャンクで透明度を保持します
- 圧縮したほうが小さい場合はcompressed alphaを使い、それ以外はraw alphaに戻します
- compressed alphaは頻度ベースのresidual符号化と、連続するresidual run、前行と一致するresidual、前行近傍と一致するresidual向けのbackward referenceを使います

## 制限

- エンコードのみ対応しています
- lossless画像サイズは各軸1から16384 pixelsの範囲が必要です
- lossy画像サイズは各軸1から16383 pixelsの範囲が必要です
- `image.NRGBA`、`image.RGBA`、`image.Gray`、`image.YCbCr`、`image.Paletted` などの標準画像型は専用の読み取り経路を使います
- それ以外の画像型は `color.NRGBAModel` 相当の変換を通してからエンコードします
- lossy alpha圧縮は意図的に単純な実装で、現時点では単一のglobal `ALPH` filter、頻度ベースのresidual符号化、連続するresidual run、前行と一致するresidual、前行近傍と一致するresidual向けの限定的なbackward referenceを使います
- general LZ77 match searchやblock-adaptive alpha entropy codingはまだ行っていません
- lossy loop filter設定は保守的で、画像固有のperceptual metricによる調整はまだ行っていません

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
