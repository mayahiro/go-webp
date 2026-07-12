package main

import "math"

type aggregateReport struct {
	NominalQuality []aggregatePoint           `json:"nominal_quality"`
	MatchedQuality []aggregatePoint           `json:"matched_quality"`
	RateDistortion rateDistortionReport       `json:"rate_distortion"`
	BySourceFormat map[string]aggregateSeries `json:"by_source_format"`
	ByAlpha        map[string]aggregateSeries `json:"by_alpha"`
}

type aggregateSeries struct {
	NominalQuality []aggregatePoint     `json:"nominal_quality"`
	MatchedQuality []aggregatePoint     `json:"matched_quality"`
	RateDistortion rateDistortionReport `json:"rate_distortion"`
}

type aggregateByteComparison struct {
	GoWebP          int     `json:"go_webp"`
	CWebP           int     `json:"cwebp"`
	GoMinusCWebP    int     `json:"go_minus_cwebp"`
	GoMinusCWebPPct float64 `json:"go_minus_cwebp_percent"`
}

type aggregatePoint struct {
	GoQuality                 int                        `json:"go_quality"`
	Fixtures                  int                        `json:"fixtures"`
	Pixels                    int64                      `json:"pixels"`
	AlphaFixtures             int                        `json:"alpha_fixtures"`
	GoBytes                   int                        `json:"go_bytes"`
	CWebPBytes                int                        `json:"cwebp_bytes"`
	GoMinusCWebPBytes         int                        `json:"go_minus_cwebp_bytes"`
	GoMinusCWebPPercent       float64                    `json:"go_minus_cwebp_percent"`
	ByteWeighted              aggregateByteComparison    `json:"byte_weighted"`
	GoEncodeMedianTotalNS     int64                      `json:"go_encode_median_total_ns"`
	CWebPProcessMedianTotalNS int64                      `json:"cwebp_process_median_total_ns"`
	GoSmaller                 int                        `json:"go_smaller"`
	CWebPSmaller              int                        `json:"cwebp_smaller"`
	EqualSize                 int                        `json:"equal_size"`
	FixtureMean               aggregateQualityComparison `json:"fixture_mean"`
	PixelWeighted             aggregateQualityComparison `json:"pixel_weighted"`
	GoAlphaExactViolations    int                        `json:"go_alpha_exact_violations"`
	CWebPAlphaExactViolations int                        `json:"cwebp_alpha_exact_violations"`
	MeanCWebPQuality          float64                    `json:"mean_cwebp_quality"`
	MinimumCWebPQuality       int                        `json:"minimum_cwebp_quality"`
	MaximumCWebPQuality       int                        `json:"maximum_cwebp_quality"`
}

type aggregateQualityComparison struct {
	YSSIM                  aggregateMetricComparison `json:"y_ssim"`
	RGBPSNRDB              aggregateMetricComparison `json:"rgb_psnr_db"`
	YPSNRDB                aggregateMetricComparison `json:"y_psnr_db"`
	UVPSNRDB               aggregateMetricComparison `json:"uv_psnr_db"`
	CompositeBlackPSNRDB   aggregateMetricComparison `json:"composite_black_psnr_db"`
	CompositeWhitePSNRDB   aggregateMetricComparison `json:"composite_white_psnr_db"`
	CompositeCheckerPSNRDB aggregateMetricComparison `json:"composite_checker_psnr_db"`
}

type aggregateMetricComparison struct {
	Go           *float64 `json:"go"`
	CWebP        *float64 `json:"cwebp"`
	GoMinusCWebP *float64 `json:"go_minus_cwebp"`
}

type aggregateMetricSums struct {
	goFixture    aggregateQualitySums
	cwebpFixture aggregateQualitySums
	goPixel      aggregateQualitySums
	cwebpPixel   aggregateQualitySums
}

type aggregateQualitySums struct {
	ySSIM               float64
	rgbMSE              float64
	yMSE                float64
	uvMSE               float64
	compositeBlackMSE   float64
	compositeWhiteMSE   float64
	compositeCheckerMSE float64
}

func (s *aggregateQualitySums) add(metrics distortionMetrics, weight float64) {
	s.ySSIM += metrics.YSSIM * weight
	s.rgbMSE += metrics.RGBMSE * weight
	s.yMSE += metrics.YMSE * weight
	s.uvMSE += metrics.UVMSE * weight
	s.compositeBlackMSE += metrics.CompositeOverBlackMSE * weight
	s.compositeWhiteMSE += metrics.CompositeOverWhiteMSE * weight
	s.compositeCheckerMSE += metrics.CompositeOverCheckerMSE * weight
}

func aggregateComparison(fixtures []fixtureReport, qualities []int) aggregateReport {
	overall := aggregateSeriesFor(fixtures, qualities)
	result := aggregateReport{
		NominalQuality: overall.NominalQuality,
		MatchedQuality: overall.MatchedQuality,
		RateDistortion: overall.RateDistortion,
		BySourceFormat: make(map[string]aggregateSeries),
		ByAlpha:        make(map[string]aggregateSeries),
	}

	bySourceFormat := make(map[string][]fixtureReport)
	byAlpha := make(map[string][]fixtureReport)
	for _, fixture := range fixtures {
		format := fixture.SourceFormat
		if format == "" {
			format = "unknown"
		}
		bySourceFormat[format] = append(bySourceFormat[format], fixture)

		alpha := "opaque"
		if fixture.HasAlpha {
			alpha = "alpha"
		}
		byAlpha[alpha] = append(byAlpha[alpha], fixture)
	}
	for format, groupedFixtures := range bySourceFormat {
		result.BySourceFormat[format] = aggregateSeriesFor(groupedFixtures, qualities)
	}
	for alpha, groupedFixtures := range byAlpha {
		result.ByAlpha[alpha] = aggregateSeriesFor(groupedFixtures, qualities)
	}
	return result
}

func aggregateSeriesFor(fixtures []fixtureReport, qualities []int) aggregateSeries {
	result := aggregateSeries{
		NominalQuality: make([]aggregatePoint, 0, len(qualities)),
		MatchedQuality: make([]aggregatePoint, 0, len(qualities)),
	}
	for _, quality := range qualities {
		result.NominalQuality = append(result.NominalQuality, aggregateQuality(fixtures, quality, false))
		result.MatchedQuality = append(result.MatchedQuality, aggregateQuality(fixtures, quality, true))
	}
	result.RateDistortion = analyzeRateDistortion(result.NominalQuality)
	return result
}

func aggregateQuality(fixtures []fixtureReport, quality int, matchQuality bool) aggregatePoint {
	result := aggregatePoint{GoQuality: quality, MinimumCWebPQuality: 101}
	sums := aggregateMetricSums{}
	var cwebpQualityTotal int
	for _, fixture := range fixtures {
		goSample, ok := sampleAtQuality(fixture.GoWebP, quality)
		if !ok {
			continue
		}
		var cwebpSample sample
		if matchQuality {
			cwebpSample, ok = nearestQualitySample(goSample, fixture.CWebP)
		} else {
			cwebpSample, ok = sampleAtQuality(fixture.CWebP, quality)
		}
		if !ok {
			continue
		}

		pixels := int64(fixture.Width) * int64(fixture.Height)
		result.Fixtures++
		if pixels > 0 {
			result.Pixels += pixels
			weight := float64(pixels)
			sums.goPixel.add(goSample.Distortion, weight)
			sums.cwebpPixel.add(cwebpSample.Distortion, weight)
		}
		sums.goFixture.add(goSample.Distortion, 1)
		sums.cwebpFixture.add(cwebpSample.Distortion, 1)
		if fixture.HasAlpha {
			result.AlphaFixtures++
		}
		if !goSample.Distortion.AlphaExact {
			result.GoAlphaExactViolations++
		}
		if !cwebpSample.Distortion.AlphaExact {
			result.CWebPAlphaExactViolations++
		}
		result.GoBytes += goSample.EncodedBytes
		result.CWebPBytes += cwebpSample.EncodedBytes
		result.GoEncodeMedianTotalNS += goSample.Timing.MedianNS
		result.CWebPProcessMedianTotalNS += cwebpSample.Timing.MedianNS
		cwebpQualityTotal += cwebpSample.Quality
		result.MinimumCWebPQuality = min(result.MinimumCWebPQuality, cwebpSample.Quality)
		result.MaximumCWebPQuality = max(result.MaximumCWebPQuality, cwebpSample.Quality)
		switch {
		case goSample.EncodedBytes < cwebpSample.EncodedBytes:
			result.GoSmaller++
		case goSample.EncodedBytes > cwebpSample.EncodedBytes:
			result.CWebPSmaller++
		default:
			result.EqualSize++
		}
	}

	result.GoMinusCWebPBytes = result.GoBytes - result.CWebPBytes
	if result.CWebPBytes != 0 {
		result.GoMinusCWebPPercent = 100 * float64(result.GoMinusCWebPBytes) / float64(result.CWebPBytes)
	}
	result.ByteWeighted = aggregateByteComparison{
		GoWebP:          result.GoBytes,
		CWebP:           result.CWebPBytes,
		GoMinusCWebP:    result.GoMinusCWebPBytes,
		GoMinusCWebPPct: result.GoMinusCWebPPercent,
	}
	if result.Fixtures == 0 {
		result.MinimumCWebPQuality = 0
		return result
	}
	result.FixtureMean = aggregateQualityFromSums(sums.goFixture, sums.cwebpFixture, float64(result.Fixtures))
	if result.Pixels > 0 {
		result.PixelWeighted = aggregateQualityFromSums(sums.goPixel, sums.cwebpPixel, float64(result.Pixels))
	}
	result.MeanCWebPQuality = float64(cwebpQualityTotal) / float64(result.Fixtures)
	return result
}

func aggregateQualityFromSums(goSums aggregateQualitySums, cwebpSums aggregateQualitySums, weight float64) aggregateQualityComparison {
	return aggregateQualityComparison{
		YSSIM:                  aggregateValueComparison(goSums.ySSIM/weight, cwebpSums.ySSIM/weight),
		RGBPSNRDB:              aggregateMSEComparison(goSums.rgbMSE/weight, cwebpSums.rgbMSE/weight),
		YPSNRDB:                aggregateMSEComparison(goSums.yMSE/weight, cwebpSums.yMSE/weight),
		UVPSNRDB:               aggregateMSEComparison(goSums.uvMSE/weight, cwebpSums.uvMSE/weight),
		CompositeBlackPSNRDB:   aggregateMSEComparison(goSums.compositeBlackMSE/weight, cwebpSums.compositeBlackMSE/weight),
		CompositeWhitePSNRDB:   aggregateMSEComparison(goSums.compositeWhiteMSE/weight, cwebpSums.compositeWhiteMSE/weight),
		CompositeCheckerPSNRDB: aggregateMSEComparison(goSums.compositeCheckerMSE/weight, cwebpSums.compositeCheckerMSE/weight),
	}
}

func aggregateValueComparison(goValue float64, cwebpValue float64) aggregateMetricComparison {
	goValueCopy := goValue
	cwebpValueCopy := cwebpValue
	delta := goValue - cwebpValue
	return aggregateMetricComparison{
		Go:           &goValueCopy,
		CWebP:        &cwebpValueCopy,
		GoMinusCWebP: &delta,
	}
}

func aggregateMSEComparison(goMSE float64, cwebpMSE float64) aggregateMetricComparison {
	goValue := aggregatePSNRDB(goMSE)
	cwebpValue := aggregatePSNRDB(cwebpMSE)
	return aggregateMetricComparison{
		Go:           goValue,
		CWebP:        cwebpValue,
		GoMinusCWebP: goMinusCWebPMetric(goValue, cwebpValue),
	}
}

func aggregatePSNRDB(mse float64) *float64 {
	if mse == 0 {
		return nil
	}
	value := 10 * math.Log10(255*255/mse)
	return &value
}
