# A consent result as Echo 1.2.0 writes it

`g01/consent.json` is the baseline probe e11 compares a run of this Echo
against: the result format of 1.2.0 must keep loading in every later
release. Until 1.2.0 ships it is the frozen contract result
(`consent/consenttest/testdata/clean.json`) moved to `g01.shop.test`.
Replace it with the `consent.json` of a real 1.2.0 run on g01 at release
(`rhythm-echo consent https://g01.shop.test/ --save base` against the
testbed) and never edit it after that.
