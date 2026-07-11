# Encoder Guide

[English](encoder.md)

このガイドではgo-webpのpublic encoding API、compression family、search profile、resource特性、現在の制約を説明します

## 対象範囲

go-webpはGo標準の `image.Image` インターフェイスから静止画WebPをencodeします
WebPのdecode、CLI、animation encodingは提供しません
WebPのdecodeには [`golang.org/x/image/webp`](https://pkg.go.dev/golang.org/x/image/webp) を使用できます

## Public API

```go
func Encode(w io.Writer, m image.Image, o *Options) error
```

`Encode` はencode結果を `w` へ書き込みます
`Options` がnilの場合とzero valueの場合はlossless WebPを選択します

```go
type Options struct {
	Compression Compression
	Quality     int
	Mode        Mode
}
```

`image/png.Encoder` に近い形式で利用できる `Encoder` も提供します

```go
type Encoder struct {
	Options *Options
}

func (enc *Encoder) Encode(w io.Writer, m image.Image) error
```

## Compression Family

| Family | 選択方法 | 挙動 |
| --- | --- | --- |
| Lossless | デフォルトまたは `CompressionLossless` | VP8Lを書き、すべてのpixelを完全に保持 |
| Lossy | `CompressionLossy` | quality 1から100のVP8 key frameを書き出す |
| Near-lossless | `ModeNearLossless` | 上限付きedge-aware RGB量子化後にVP8Lを書き、alphaを保持 |
| Explicit lossy | `ModeLossyQuality` | `Compression` に関係なくVP8 lossyを書き出す |

lossy encodingでは `Quality` が0以下の場合はpackage defaultを使い、100を超える場合は100へ丸めます
通常のlossless encodingでは `Quality` を無視します

near-lossless qualityに対するRGB各channelの最大誤差は次のとおりです

| Quality | RGB channelの最大誤差 |
| ---: | ---: |
| 100または未指定 | 0 |
| 80-99 | 1 |
| 60-79 | 2 |
| 40-59 | 4 |
| 20-39 | 8 |
| 1-19 | 16 |

幅と高さがともに64 pixels未満のnear-lossless入力、または高さが3 pixels未満の入力は変更しません

## Search Profile

| Mode | 目的 |
| --- | --- |
| `ModeDefault` | `Compression` と `Quality` で選ばれる挙動を使用 |
| `ModeFast` | searchと保持するstateを削減 |
| `ModeBalanced` | size、quality、speed、memoryの標準的なbalanceを使用 |
| `ModeBestCompression` | 上限付きcompression候補を追加評価 |
| `ModeLowMemory` | full-frame source、residual、token、cache bufferを回避 |
| `ModeNearLossless` | VP8L near-lossless encodingを選択 |
| `ModeLossyQuality` | VP8 lossy encodingを選択 |
| `ModeAuto` | 画像特徴から保守的な内部profileを選択 |

すべての画像で最小または最速になることを保証するprofileはありません
`ModeFast` と `ModeLowMemory` は意図的にsearchを減らすため、出力が大きくなる場合があります

Balanced lossless profileはlow-color画像でcolor indexingが明確に有利な場合にtransform searchを終了できます
`ModeBestCompression` はより広い上限付きtransform searchを行い、`ModeAuto` は非常に小さいindexed payloadの場合だけfast lossless profileを選択します

## Lossless Encoding

lossless encoderはVP8L predictor、color、color indexing、subtract-green、palette transformを評価し、推定した全体bit costが小さくなる場合に使用します
上限付きsearchには次を含みます

- constant channel向けsingle-symbol Huffman tree
- 256 symbols全体のchannel histogramとnormal Huffman tree
- 小さいlow-color入力向けの上限付きpacked color-index stream
- 複数候補LZ77 match finderと1手先のlazy matching
- indexed streamと有望な一般画像stream向けのcost-based optimal parsing
- 65,536 pixels以上のbuffered finalist stream向け上限付きcompact match graph
- 1回のsource走査で解析し、1から11 bitsから動的に選択するcolor cache
- literalと変換済みresidual stream向けのspatial prediction、LZ77、color cache codingの選択的な組み合わせ
- tile適応のpredictor modeとcross-color係数
- 最大32 coding groupsのentropy-clustered meta-prefix histogram
- cheapなshortlist後にoptimal LZ77、color cache、histogram候補の完全な出力costを比較する2段階planner
- finalist候補間で再利用するencode単位のtoken、hash、dynamic-programming workspace

`ModeBestCompression` はcolor-cache hit costをmodelへ含めたoptimal LZ77 parsingを追加実行できます
元の候補も維持し、完全な出力costが小さくなる場合だけこのpassを採用します

制限なしのhash chainは使用しません
これによりworkとmemoryを制限できますが、より広いsearchを行うencoderより出力が大きくなる入力もあります

## Lossy Encoding

lossy encoderは4:2:0 chromaを持つintra-only VP8 key frameを書きます
上限付き解析には次を含みます

- adaptive chroma downsamplingと任意のsharp chroma変換
- intra16x16、chroma、任意のluma4x4 prediction mode
- 最大4個のactivity適応quantizer segment
- quality依存quantization biasと、lumaおよびchroma別rate-distortion weight
- 中品質向けの上限付きspectral texture項
- residual bufferを利用できる場合のmacroblock skip signalingとresidual token probabilityのjoint選択
- quality依存設定とluma4x4 mode deltaを持つnormal VP8 loop filter

`ModeFast` と `ModeLowMemory` はluma4x4 mode searchを省きます
`ModeFast` はmacroblock skipとtoken-probability update searchも省きます
`ModeBestCompression` は2回目のrate-distortion pass、trellis quantization、sharp chroma searchを追加します

## Alpha

losslessとnear-losslessはalphaを完全に保持します
alpha付きlossy画像は `ALPH` chunkを持つextended WebPとして書き出します
encoderはcompressed alphaとraw alphaを比較し、小さい表現を使用します

compressed lossy alphaはglobal filter、frequency-coded residual、連続runと前行spatial match向けの上限付きbackward referenceを使います
`ModeFast` はunfiltered alphaと連続runへsearchを限定します
`ModeLowMemory` はfilter searchを維持しつつ前行候補を省き、`ModeBestCompression` はrunと前行候補へ上限付きoptimal parsingを適用します
完全にopaqueな標準画像型ではalpha候補解析を省き、custom画像実装では一般的な解析pathを使います

## InputとResource特性

`image.NRGBA`、`image.RGBA`、`image.Gray`、`image.YCbCr`、`image.Paletted` は専用のread pathを使います
その他の画像実装は `color.NRGBAModel` 相当の変換を通して読み取ります

encodingでは入力を複数回走査する場合があります
主なresource上限は次のとおりです

- bufferを使うlossless profileは最大32 MiBのsource planeと変換済みfinalist pixel planeを使う場合がある
- lossless compact match graphは最大32 MiBで、対象size外では上限付きdirect matcherへfallbackする
- bufferを使うlossy profileは推定32 MiBまで量子化済みresidualを保持し、統計と最終codingで再利用できる
- lossyの再構成pixelはfull-frame planeではなく2 macroblock rowsのringを使う
- `ModeBestCompression` は独立したlossless候補を最大4 workersで解析でき、並行readの安全性が既知でないcustom画像実装には最大32 MiBの変換済みplaneを使う
- 標準画像型ではlossy frame planningとalpha解析を2 workersで実行できる
- `ModeLowMemory` はfull-frame source plane、VP8 residual buffer、VP8L token stream、meta-prefix plan、color-cache planを保持しない

buffer上限を超える入力では、上限付きの反復passまたは逐次pathへfallbackします
標準画像型は可能な場合にdirect readerを使います
小さい画像、custom画像実装、single-thread runtimeではlossy frameとalphaを逐次解析します

## 制約

- lossless画像の各軸は1から16384 pixels
- lossy画像の各軸は1から16383 pixels
- 静止画だけをencodeする
- `image.Image` APIでは画像metadataを保持しない
- lossy alphaではgeneral hash-chain LZ77 searchやblock-adaptive alpha entropy codingを行わない
- lossy loop-filter設定は保守的で、画像固有のperceptual optimization passでは選択していない
