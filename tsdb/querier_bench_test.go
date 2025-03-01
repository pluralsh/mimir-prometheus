// Copyright 2018 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tsdb

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb/hashcache"
)

// Make entries ~50B in size, to emulate real-world high cardinality.
const (
	postingsBenchSuffix = "aaaaaaaaaabbbbbbbbbbccccccccccdddddddddd"
)

func BenchmarkQuerier(b *testing.B) {
	chunkDir := b.TempDir()
	opts := DefaultHeadOptions()
	opts.ChunkRange = 1000
	opts.ChunkDirRoot = chunkDir
	h, err := NewHead(nil, nil, nil, nil, opts, nil)
	require.NoError(b, err)
	defer func() {
		require.NoError(b, h.Close())
	}()

	app := h.Appender(context.Background())
	addSeries := func(l labels.Labels) {
		app.Append(0, l, 0, 0)
	}

	for n := 0; n < 10; n++ {
		for i := 0; i < 100000; i++ {
			addSeries(labels.FromStrings("i", strconv.Itoa(i)+postingsBenchSuffix, "n", strconv.Itoa(n)+postingsBenchSuffix, "j", "foo"))
			// Have some series that won't be matched, to properly test inverted matches.
			addSeries(labels.FromStrings("i", strconv.Itoa(i)+postingsBenchSuffix, "n", strconv.Itoa(n)+postingsBenchSuffix, "j", "bar"))
			addSeries(labels.FromStrings("i", strconv.Itoa(i)+postingsBenchSuffix, "n", "0_"+strconv.Itoa(n)+postingsBenchSuffix, "j", "bar"))
			addSeries(labels.FromStrings("i", strconv.Itoa(i)+postingsBenchSuffix, "n", "1_"+strconv.Itoa(n)+postingsBenchSuffix, "j", "bar"))
			addSeries(labels.FromStrings("i", strconv.Itoa(i)+postingsBenchSuffix, "n", "2_"+strconv.Itoa(n)+postingsBenchSuffix, "j", "foo"))
		}
	}
	require.NoError(b, app.Commit())

	ir, err := h.Index()
	require.NoError(b, err)
	b.Run("Head", func(b *testing.B) {
		b.Run("PostingsForMatchers", func(b *testing.B) {
			benchmarkPostingsForMatchers(b, ir)
		})
		b.Run("labelValuesWithMatchers", func(b *testing.B) {
			benchmarkLabelValuesWithMatchers(b, ir)
		})
	})

	tmpdir := b.TempDir()

	blockdir := createBlockFromHead(b, tmpdir, h)
	block, err := OpenBlock(nil, blockdir, nil)
	require.NoError(b, err)
	defer func() {
		require.NoError(b, block.Close())
	}()
	ir, err = block.Index()
	require.NoError(b, err)
	defer ir.Close()
	b.Run("Block", func(b *testing.B) {
		b.Run("PostingsForMatchers", func(b *testing.B) {
			benchmarkPostingsForMatchers(b, ir)
		})
		b.Run("labelValuesWithMatchers", func(b *testing.B) {
			benchmarkLabelValuesWithMatchers(b, ir)
		})
	})
}

func benchmarkPostingsForMatchers(b *testing.B, ir IndexReader) {
	n1 := labels.MustNewMatcher(labels.MatchEqual, "n", "1"+postingsBenchSuffix)
	nX := labels.MustNewMatcher(labels.MatchEqual, "n", "X"+postingsBenchSuffix)

	jFoo := labels.MustNewMatcher(labels.MatchEqual, "j", "foo")
	jNotFoo := labels.MustNewMatcher(labels.MatchNotEqual, "j", "foo")

	iStar := labels.MustNewMatcher(labels.MatchRegexp, "i", "^.*$")
	i1Star := labels.MustNewMatcher(labels.MatchRegexp, "i", "^1.*$")
	iStar1 := labels.MustNewMatcher(labels.MatchRegexp, "i", "^.*1$")
	iStar1Star := labels.MustNewMatcher(labels.MatchRegexp, "i", "^.*1.*$")
	iPlus := labels.MustNewMatcher(labels.MatchRegexp, "i", "^.+$")
	i1Plus := labels.MustNewMatcher(labels.MatchRegexp, "i", "^1.+$")
	iEmptyRe := labels.MustNewMatcher(labels.MatchRegexp, "i", "^$")
	iNotEmpty := labels.MustNewMatcher(labels.MatchNotEqual, "i", "")
	iNot2 := labels.MustNewMatcher(labels.MatchNotEqual, "i", "2"+postingsBenchSuffix)
	iNot2Star := labels.MustNewMatcher(labels.MatchNotRegexp, "i", "^2.*$")
	iNotStar2Star := labels.MustNewMatcher(labels.MatchNotRegexp, "i", "^.*2.*$")
	jFooBar := labels.MustNewMatcher(labels.MatchRegexp, "j", "foo|bar")
	jXXXYYY := labels.MustNewMatcher(labels.MatchRegexp, "j", "XXX|YYY")
	jXplus := labels.MustNewMatcher(labels.MatchRegexp, "j", "X.+")
	iCharSet := labels.MustNewMatcher(labels.MatchRegexp, "i", "1[0-9]")
	iAlternate := labels.MustNewMatcher(labels.MatchRegexp, "i", "(1|2|3|4|5|6|20|55)")
	iXYZ := labels.MustNewMatcher(labels.MatchRegexp, "i", "X|Y|Z")
	cases := []struct {
		name     string
		matchers []*labels.Matcher
	}{
		{`n="1"`, []*labels.Matcher{n1}},
		{`n="X"`, []*labels.Matcher{nX}},
		{`n="1",j="foo"`, []*labels.Matcher{n1, jFoo}},
		{`n="X",j="foo"`, []*labels.Matcher{nX, jFoo}},
		{`j="foo",n="1"`, []*labels.Matcher{jFoo, n1}},
		{`n="1",j!="foo"`, []*labels.Matcher{n1, jNotFoo}},
		{`n="X",j!="foo"`, []*labels.Matcher{nX, jNotFoo}},
		{`i=~"1[0-9]",j=~"foo|bar"`, []*labels.Matcher{iCharSet, jFooBar}},
		{`j=~"foo|bar"`, []*labels.Matcher{jFooBar}},
		{`j=~"XXX|YYY"`, []*labels.Matcher{jXXXYYY}},
		{`j=~"X.+"`, []*labels.Matcher{jXplus}},
		{`i=~"(1|2|3|4|5|6|20|55)"`, []*labels.Matcher{iAlternate}},
		{`i=~"X|Y|Z"`, []*labels.Matcher{iXYZ}},
		{`i=~".*"`, []*labels.Matcher{iStar}},
		{`i=~"1.*"`, []*labels.Matcher{i1Star}},
		{`i=~".*1"`, []*labels.Matcher{iStar1}},
		{`i=~".+"`, []*labels.Matcher{iPlus}},
		{`i=~".+",j=~"X.+"`, []*labels.Matcher{iPlus, jXplus}},
		{`i=~""`, []*labels.Matcher{iEmptyRe}},
		{`i!=""`, []*labels.Matcher{iNotEmpty}},
		{`n="1",i=~".*",j="foo"`, []*labels.Matcher{n1, iStar, jFoo}},
		{`n="X",i=~".*",j="foo"`, []*labels.Matcher{nX, iStar, jFoo}},
		{`n="1",i=~".*",i!="2",j="foo"`, []*labels.Matcher{n1, iStar, iNot2, jFoo}},
		{`n="1",i!=""`, []*labels.Matcher{n1, iNotEmpty}},
		{`n="1",i!="",j="foo"`, []*labels.Matcher{n1, iNotEmpty, jFoo}},
		{`n="1",i!="",j=~"X.+"`, []*labels.Matcher{n1, iNotEmpty, jXplus}},
		{`n="1",i!="",j=~"XXX|YYY"`, []*labels.Matcher{n1, iNotEmpty, jXXXYYY}},
		{`n="1",i=~"X|Y|Z",j="foo"`, []*labels.Matcher{n1, iXYZ, jFoo}},
		{`n="1",i=~".+",j="foo"`, []*labels.Matcher{n1, iPlus, jFoo}},
		{`n="1",i=~"1.+",j="foo"`, []*labels.Matcher{n1, i1Plus, jFoo}},
		{`n="1",i=~".*1.*",j="foo"`, []*labels.Matcher{n1, iStar1Star, jFoo}},
		{`n="1",i=~".+",i!="2",j="foo"`, []*labels.Matcher{n1, iPlus, iNot2, jFoo}},
		{`n="1",i=~".+",i!~"2.*",j="foo"`, []*labels.Matcher{n1, iPlus, iNot2Star, jFoo}},
		{`n="1",i=~".+",i!~".*2.*",j="foo"`, []*labels.Matcher{n1, iPlus, iNotStar2Star, jFoo}},
		{`n="X",i=~".+",i!~".*2.*",j="foo"`, []*labels.Matcher{nX, iPlus, iNotStar2Star, jFoo}},
	}

	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, err := PostingsForMatchers(ir, c.matchers...)
				require.NoError(b, err)
			}
		})
	}
}

func benchmarkLabelValuesWithMatchers(b *testing.B, ir IndexReader) {
	i1 := labels.MustNewMatcher(labels.MatchEqual, "i", "1")
	iStar := labels.MustNewMatcher(labels.MatchRegexp, "i", "^.*$")
	jNotFoo := labels.MustNewMatcher(labels.MatchNotEqual, "j", "foo")
	jXXXYYY := labels.MustNewMatcher(labels.MatchRegexp, "j", "XXX|YYY")
	jXplus := labels.MustNewMatcher(labels.MatchRegexp, "j", "X.+")
	n1 := labels.MustNewMatcher(labels.MatchEqual, "n", "1"+postingsBenchSuffix)
	nX := labels.MustNewMatcher(labels.MatchNotEqual, "n", "X"+postingsBenchSuffix)
	nPlus := labels.MustNewMatcher(labels.MatchRegexp, "i", "^.+$")

	cases := []struct {
		name      string
		labelName string
		matchers  []*labels.Matcher
	}{
		// i has 100k values.
		{`i with n="1"`, "i", []*labels.Matcher{n1}},
		{`i with n="^.+$"`, "i", []*labels.Matcher{nPlus}},
		{`i with n="1",j!="foo"`, "i", []*labels.Matcher{n1, jNotFoo}},
		{`i with n="1",j=~"X.+"`, "i", []*labels.Matcher{n1, jXplus}},
		{`i with n="1",j=~"XXX|YYY"`, "i", []*labels.Matcher{n1, jXXXYYY}},
		{`i with n="X",j!="foo"`, "i", []*labels.Matcher{nX, jNotFoo}},
		{`i with n="1",i=~"^.*$",j!="foo"`, "i", []*labels.Matcher{n1, iStar, jNotFoo}},
		// n has 10 values.
		{`n with j!="foo"`, "n", []*labels.Matcher{jNotFoo}},
		{`n with i="1"`, "n", []*labels.Matcher{i1}},
	}

	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, err := labelValuesWithMatchers(ir, c.labelName, c.matchers...)
				require.NoError(b, err)
			}
		})
	}
}

func BenchmarkQuerierSelect(b *testing.B) {
	chunkDir := b.TempDir()
	opts := DefaultHeadOptions()
	opts.ChunkRange = 1000
	opts.ChunkDirRoot = chunkDir
	h, err := NewHead(nil, nil, nil, nil, opts, nil)
	require.NoError(b, err)
	defer h.Close()
	app := h.Appender(context.Background())
	numSeries := 1000000
	for i := 0; i < numSeries; i++ {
		app.Append(0, labels.FromStrings("foo", "bar", "i", fmt.Sprintf("%d%s", i, postingsBenchSuffix)), int64(i), 0)
	}
	require.NoError(b, app.Commit())

	bench := func(b *testing.B, br BlockReader, sorted, sharding bool) {
		matcher := labels.MustNewMatcher(labels.MatchEqual, "foo", "bar")
		for s := 1; s <= numSeries; s *= 10 {
			b.Run(fmt.Sprintf("%dof%d", s, numSeries), func(b *testing.B) {
				mint := int64(0)
				maxt := int64(s - 1)
				q, err := NewBlockQuerier(br, mint, maxt)
				require.NoError(b, err)

				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					var hints *storage.SelectHints
					if sharding {
						hints = &storage.SelectHints{
							Start:      mint,
							End:        maxt,
							ShardIndex: uint64(i % 16),
							ShardCount: 16,
						}
					}

					ss := q.Select(sorted, hints, matcher)
					for ss.Next() {
					}
					require.NoError(b, ss.Err())
				}
				q.Close()
			})
		}
	}

	b.Run("Head", func(b *testing.B) {
		b.Run("without sharding", func(b *testing.B) {
			bench(b, h, false, false)
		})
		b.Run("with sharding", func(b *testing.B) {
			bench(b, h, false, true)
		})
	})
	b.Run("SortedHead", func(b *testing.B) {
		b.Run("without sharding", func(b *testing.B) {
			bench(b, h, true, false)
		})
		b.Run("with sharding", func(b *testing.B) {
			bench(b, h, true, true)
		})
	})

	tmpdir := b.TempDir()

	seriesHashCache := hashcache.NewSeriesHashCache(1024 * 1024 * 1024)
	blockdir := createBlockFromHead(b, tmpdir, h)
	block, err := OpenBlockWithOptions(nil, blockdir, nil, seriesHashCache.GetBlockCacheProvider("test"), defaultPostingsForMatchersCacheTTL, defaultPostingsForMatchersCacheSize, false)
	require.NoError(b, err)
	defer func() {
		require.NoError(b, block.Close())
	}()

	b.Run("Block", func(b *testing.B) {
		b.Run("without sharding", func(b *testing.B) {
			bench(b, block, false, false)
		})
		b.Run("with sharding", func(b *testing.B) {
			bench(b, block, false, true)
		})
	})
}
