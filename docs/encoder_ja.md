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
| `ModeAuto` | encoderに内部profileの選択を委ねる |

すべての画像で最小または最速になることを保証するprofileはありません
`ModeFast` と `ModeLowMemory` は意図的にsearchを減らすため、出力が大きくなる場合があります

Balanced lossless profileは標準の上限付きtransform、match、entropy budgetを使用します
`ModeBestCompression` は各budgetを広げ、`ModeAuto` は検証済みindexed payloadが非常に小さい場合だけfast lossless profileを選択します

lossy encodingでは、現在の `ModeAuto` は `ModeDefault` と同じquality、effort、alpha設定を使用します
これは現在のroutingであり、永続的なaliasやserialization contractではありません
将来のreleaseでは `ModeAuto` が別のlossy profileを選択する可能性があります
同じencoder version、入力、optionsの組み合わせでは決定的な出力を生成します

## Lossless Encoding

lossless encoderは各機能を個別に選ぶのではなく、完全なVP8L planを比較します
bufferを使うprofileは次の段階的な上限付きsearchを行います

- direct、subtract-green、predictor、cross-color、paletteと選択的な組み合わせを含むtransform graph
- tile適応predictor modeと、`ModeBestCompression` で任意に使うblock適応cross-color係数
- exact literal bitsとlocal、distant match potentialを組み合わせるfamily-awareなcost-only Huffman screening
- 小さなfinalist集合へexact matchとdynamic-programming parseを行う前のshallow parse
- 上限付きhash chainとrun、前行matchの伝播を持つcompactな逆向きmatch graph
- literalとbackward referenceを比較するcost-based dynamic-programming parserと、1から11 bitsのcolor cacheの1 pass screening
- 上限付きincumbent集合に対するsparse spatial histogramと最大16 coding groupsのentropy clustering
- emissionと同じbit writerを使うexact size比較
- finalist間で再利用するencode単位のtransform、match、Huffman、cache、entropy、parser workspace

`ModeFast` と `ModeLowMemory` はcheapなtransformとgreedy matchを持つrow-streaming pathを使用します
bufferを使うmodeでもsourceまたは推定search workspaceが設定上限を超える場合はstreamingへfallbackします

`ModeBestCompression` はDefaultの完全planをincumbentとして保持し、同じsearch sessionでbudgetを拡張します
source pixels、palette解析、candidate score、Default winnerは再利用されます
拡張結果のexact payloadが小さい場合だけ置き換えるため、同じ入力で `ModeDefault` より大きいlossless payloadを出力しません

matcherは制限なしのhash chainを使いません
各profileはcandidate数、edge数、parse反復、entropy group、worker、workspace推定を制限するため、より広いsearchを行うencoderより出力が大きくなる入力もあります

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
`ModeBestCompression` は `ModeDefault` と同じquality objectiveを使い、sharp chromaを常に有効にし、2回目のrate-distortion passとresidual token probability学習後のluma4x4 modeに対する幅2の上限付きrefinementを追加します
完全な `ModeDefault` VP8 frameをexact-size incumbentとして保持し、追加searchのframeが小さい場合だけ置き換えます
同じqualityで大きいVP8 frameを防ぐ一方、両planを評価するため大幅に時間がかかる場合があります

VP8のfirst partition lengthは19 bitsです
選択したlossy planが上限を超える場合、encoderはfirst partition signalingを段階的に減らして決定的に再試行します
token probability updateの削除、segmentationの縮小と無効化、luma4x4 searchの制限と無効化を順に行い、それでも収まらない場合だけDC predictionの緊急planを使用します
最初から上限内に収まるplanのbitstreamは変更しません

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

lossy encodingでは `image.YCbCr` のfull-range Y、Cb、Cr planeからVP8 limited-range YUVへRGB round tripなしで直接変換します
Goの全subsampling ratioをratio別readerで処理します

encodingでは入力を複数回走査する場合があります
主なresource上限は次のとおりです

- bufferを使うlossless profileは最大32 MiBのpacked source planeを使用する
- Defaultのbuffered lossless searchは96 MiBの推定workspace gateと最大2 finalist workersを持つ
- `ModeBestCompression` は192 MiBの推定workspace gateと最大4 finalist workersを持つ
- buffered lossless gateの範囲外の入力はrow-streaming encodingへfallbackする
- lossless screeningは再生成可能なtransform descriptorを保持し、exact finalistのtransform chainはencode単位のscratch buffer間を交互に使い、逐次または上限付きworker poolから再利用する
- bufferを使うlossy profileは推定32 MiBまで量子化済みresidualを保持し、統計と最終codingで再利用できる
- lossyの再構成pixelはfull-frame planeではなく2 macroblock rowsのringを使う
- 標準画像型ではlossy frame planningとalpha解析を2 workersで実行できる
- VP8 first partitionが上限を超える場合は上限付きの逐次再planを行うが、通常planではこの追加処理を行わない
- `ModeLowMemory` はfull-frame source plane、VP8 residual buffer、VP8L token stream、meta-prefix plan、color-cache planを保持しない

Go benchmarkの `B/op` はpeak live memoryではなく累積allocationを表します
逐次生成するcandidate metadata、token、coding structureにより累積allocationが同時保持stateを超える場合があります
標準画像型は可能な場合にdirect readerを使います

encoded bytesは安定したserialization contractではありません
encoder versionによって異なる有効なVP8LまたはVP8 planを選択する場合がありますが、文書化したpixel behaviorとpublic APIは維持します

## 制約

- lossless画像の各軸は1から16384 pixels
- lossy画像の各軸は1から16383 pixels
- 静止画だけをencodeする
- `image.Image` APIでは画像metadataを保持しない
- lossy alphaではgeneral hash-chain LZ77 searchやblock-adaptive alpha entropy codingを行わない
- lossy loop-filter設定は保守的で、画像固有のperceptual optimization passでは選択していない
