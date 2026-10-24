# CI fixture

A one-page shop that sends one email, for Echo's own CI: the GitHub Action's tests (`.github/workflows/action-test.yml`), the Windows and macOS jobs in `ci.yml` and `go test ./integrations/...`. It needs only PHP.

```sh
php -S 127.0.0.1:20510 php/index.php   # in this folder
rhythm-echo up --seal &                 # another terminal
rhythm-echo snapshot --name good        # in this folder
touch php/broken                        # the shop stops sending the confirmation
rhythm-echo compare                     # one BROKE, one NEW
```

`php/broken` switches the shop to its broken state; delete it to switch back. It is in `.gitignore`.
