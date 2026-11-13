package transcript

import (
	"testing"
)

func TestVTTSentencesAndSpeakers(t *testing.T) {
	vtt := `WEBVTT
Kind: captions
Language: en

00:00:00.000 --> 00:00:07.000
Transcriber: Joseph Geni Reviewer: Morton Bast

00:00:12.863 --> 00:00:16.063
When I was 27 years old, I left a very demanding job

00:00:16.063 --> 00:00:22.063
in management consulting &amp; went to teach.

00:00:23.000 --> 00:00:25.000
<v Ana>How are you?

00:00:25.000 --> 00:00:27.000
<v Ben>Fine, thanks.

00:00:27.000 --> 00:00:28.000
(Applause)
`
	cues, err := Parse("vtt", []byte(vtt))
	if err != nil {
		t.Fatal(err)
	}
	lines := Sentences(cues)
	if len(lines) != 4 {
		t.Fatalf("got %d lines: %+v", len(lines), lines)
	}
	if lines[0].Text != "When I was 27 years old, I left a very demanding job in management consulting & went to teach." || lines[0].StartMs != 12863 || *lines[0].EndMs != 22063 {
		t.Fatalf("first sentence = %+v", lines[0])
	}
	if lines[1].Speaker == nil || *lines[1].Speaker != "Ana" || *lines[2].Speaker != "Ben" {
		t.Fatalf("speakers = %+v %+v", lines[1], lines[2])
	}
}

func TestSRT(t *testing.T) {
	srt := "1\r\n00:00:01,500 --> 00:00:03,000\r\nJOHN: Hello there.\r\n\r\n2\r\n00:00:03,000 --> 00:00:04,000\r\nMARY: Hi.\r\n"
	cues, err := Parse("srt", []byte(srt))
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 2 || cues[0].StartMs != 1500 || *cues[0].Speaker != "JOHN" || cues[0].Text != "Hello there." {
		t.Fatalf("cues = %+v", cues)
	}
}

func TestAutoCaptionsWithoutPunctuationSplitOnPauses(t *testing.T) {
	// Shape of YouTube json3 auto captions: aAppend events carry only "\n".
	j := `{"events":[
	 {"tStartMs":0,"dDurationMs":5000,"segs":[{"utf8":"so today we"},{"utf8":" talk about sleep"}]},
	 {"tStartMs":2000,"dDurationMs":3000,"aAppend":1,"segs":[{"utf8":"\n"}]},
	 {"tStartMs":2500,"dDurationMs":2000,"segs":[{"utf8":"and why it matters"}]},
	 {"tStartMs":6000,"dDurationMs":2000,"segs":[{"utf8":"first the basics"}]}
	]}`
	cues, err := Parse("json3", []byte(j))
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 3 || *cues[0].EndMs != 2500 {
		t.Fatalf("cues = %+v", cues)
	}
	lines := Sentences(cues)
	if len(lines) != 2 || lines[0].Text != "so today we talk about sleep and why it matters" {
		t.Fatalf("lines = %+v", lines)
	}
}
