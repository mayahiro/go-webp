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

`Mode` はencoderの探索幅を調整するか、明示的な出力形式を選びます

```go
err := webp.Encode(w, img, &webp.Options{
	Mode:    webp.ModeNearLossless,
	Quality: 75,
})
```

`ModeDefault` は `Compression` と `Quality` で選ばれる既存挙動を維持します
`ModeFast`、`ModeBalanced`、`ModeBestCompression`、`ModeLowMemory`、`ModeAuto` は選択中のcompression modeの探索幅を調整します
`ModeNearLossless` はalphaを保持し、`Quality` に応じてRGBを量子化したVP8Lを書き出します。quality 100、または未指定のqualityはlossless相当です
`ModeLossyQuality` は `Compression` に関係なくVP8 lossy出力を書き、`Quality` を使います

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
- lossless encodingでは入力画像を複数回走査し、変換済み画像全体は保持しません
- 小さいlow-color画像では、LZ77評価時の色参照を繰り返さないよう、上限付きのpacked color-index streamを保持する場合があります
- 単一値のチャンネルはsingle-symbol Huffman treeでエンコードします
- lossless encoderは出力サイズ削減が見込める場合、VP8L predictor transform、color transform、color indexing transform、subtract-green transformを使います。`ModeBestCompression` ではblock-adaptive predictor候補も試します
- 固定幅の複数候補hash match finderと1手先のlazy matchingによる単純なVP8L LZ77 backwards referencesも使えます
- sampleとbit costの推定で有利と判断した場合、literal stream向けの限定的なVP8L color cacheも使えます。transformなしのstreamでは、固定幅LZ77とcolor cacheの併用もできます
- predictorやcolor transform後の一部residual streamでも、条件付きでLZ77とcolor cacheを併用できます
- `ModeBestCompression` では、領域ごとのentropy差に対応するため、上限付きのtoken主導meta-prefix histogram grouping候補を1つ追加比較します
- 制限なしのhash chainは使わないため、高度に最適化されたWebP encoderよりlossless出力が大きくなることがあります
- `ModeFast` と `ModeLowMemory` は意図的にlossless探索を減らすため、VP8L出力が大きくなる場合があります
- Balanced profileはlow-color画像でcolor indexingが明確に有利な場合にtransform探索を早期終了し、`ModeBestCompression` は全transform探索を維持します
- `ModeAuto` は保守的な画像特徴の判定を使い、losslessのFast profileはindexed payloadが十分小さい場合だけ選びます。すべての画像で最小または最速の出力を保証するものではありません
- alpha付きlossy画像では、`ModeFast` は `ALPH` 探索をunfiltered alphaとrun符号化に限定し、`ModeLowMemory` はfilter探索を維持しつつ前行空間参照候補を省きます
- lossy encodingでは標準画像型のopacity判定を使い、完全にopaqueな入力では `ALPH` 候補解析を省きます。custom画像型は従来のpixel解析経路を使います
- lossy VP8出力では、`ModeFast` は指定されたquality mappingを維持しつつ、macroblock skip signalingとtoken probability update探索を無効化します。`ModeBestCompression` は追加でluma4x4 mode探索を有効化します
- skipまたはtoken probability解析を使うlossy profileでは、選択済みの量子化residualを保持し、統計と最終符号化で再利用することでmacroblockのDCTと再構築の反復を削減します
- このbufferは推定32 MiBを上限とし、`ModeFast`、`ModeLowMemory`、上限を超える画像ではbufferを使わない反復passへ戻ります
- lossy encodingは4:2:0 chroma subsampling、adaptive chroma downsampling、選択されたintra16x16とchroma prediction mode、`ModeBestCompression` で使う任意のluma4x4 mode、量子化されたDC/AC係数を使う低複雑度VP8 key frame encoderです
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

任意の外部decoder確認:

```sh
go run ./scripts/verify_lossless_external
```

外部decoder確認はlossless fixtureをpixel完全一致で検証し、lossy fixtureはRGB誤差の上限内で検証します
`dwebp` が利用できる場合は優先して使い、それ以外では一時的な `golang.org/x/image/webp` decoderを `go run` で使います
どちらも利用できない場合のみmacOSの `sips` にfallbackします

libwebpとのlossless比較をローカルで行う場合:

```sh
go run ./scripts/compare_lossless_libwebp -runs 3
```

## ライセンス

MIT
