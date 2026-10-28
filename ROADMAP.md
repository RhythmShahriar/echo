# Roadmap

What we plan for Echo. Plans change as we learn, so treat this as a direction, not a promise.

Have an idea or a service Echo should recognize? Start a thread in [Discussions: Ideas](https://github.com/RhythmShahriar/echo/discussions/categories/ideas). Want to help now? Pick a [good first issue](https://github.com/RhythmShahriar/echo/labels/good%20first%20issue). A new provider is often one YAML file and its fixtures.

## Now

- **Cookie consent checks (1.2).** `rhythm echo consent` clicks a cookie banner three ways in a real browser and shows every tracker that fired before the choice or after Reject, with Consent Mode decoded, an evidence folder and the Action's `mode: consent`.
- **Echo for WordPress on WordPress.org.** The connector as a plugin you install from the directory.
- **Published images.** The Docker image on ghcr.io and a DDEV add-on in the DDEV registry.

## Next

- **More providers.** Grow the catalogue of email, SMS, chat, push and analytics services, each as a YAML file with fixtures.
- **Connector tests on every pull request.** Run the WordPress, Laravel and Node suites in CI with fixtures for every provider they wrap.
- **Late mail with no Date header.** A capture time in the wire format so it is attributed to the right action.
- **Attachment changes.** An opt-in to report an attachment whose content changed but whose size barely moved.
- **Outside benchmark.** A rerunnable comparison with other catchers on the same scenarios, run by someone other than us.
- **Consent: the rest of the testbed.** A scenario that makes Reject itself fail (`consent/reject-broken` is caught today only by unit fixtures), the Docker variants of c15 and c18 and a sealed Docker benchmark run to stand next to the Go-mode numbers.
- **Consent: clearer counts.** With `--against`, the verdict counts each page, run and tracker while the lines group them; the verdict should count what it lists.
- **Consent: a version in source builds.** `make build` stamps `dev`; a version from the nearest tag would let a source build say which release it matches.

## Later

- **CRM as its own channel.** Today CRM calls count as webhooks.
- **Update night with Tuner and Soundcheck.** One run that says which plugin update stopped which message.
- **One rule format.** Echo custom rules, Tuner checks and Soundcheck error rules share one YAML loader.
- **Agency fleet file.** One `sites.yaml` for the hub, Tuner and Soundcheck.
- **Consent in Soundcheck.** Soundcheck dismisses cookie banners with Echo's CMP rules before its screenshots; the browser layer both use moves to rhythm's `kit/browser`.
- **Consent everywhere in Echo.** A consent column in the hub's agency report, `rhythm echo schedule` consent jobs and keyless signature checks inside the binary.
- **Consent reach.** A Chrome container image, importing autoconsent rule files and IP geolocation (today `--geo` sets the language and time zone only).
- **One CLI contract.** The rest of the move onto rhythm's `kit/cli.Main`: shared exit codes and the update prompt. `version --json` and completion came in 1.2.
