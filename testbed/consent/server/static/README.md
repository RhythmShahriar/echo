# Consent testbed static files

What the testbed server serves on the stand-in hosts, embedded in the
server binary:

- `tags/`: stand-ins for the tracker tags (Google, Meta, TikTok, LinkedIn,
  Microsoft, Hotjar, Plausible, YouTube), written to the behaviour the
  vendors and the community document. No vendor code.
- `cmps/`: replicas of the commercial consent banners, written fresh from
  each vendor's public markup and documented JavaScript API, with a
  `SOURCE.md` per replica; `cmps/vendor/` holds the open-source CMPs used
  as they are, with their licences.
