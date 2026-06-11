# Eval-time tests for the catalog schema rules (v0.5 "Library", P1
# catalog-as-tests). Mirrors the assertions in modules/platform.nix so a bad
# catalog declaration fails `nix flake check`, not just a host build.
{ lib }:

let
  validTrust = [ "official" "community" "untrusted" ];
  validPolicy = [ "strict" "warn" ];
  validCategory = [ "media" "network" "dev" "data" "monitoring" "misc" ];
  movingRefs = [ "latest" "stable" "main" "master" "release" "edge" "HEAD" ];

  # A single entry is well-formed iff every rule holds.
  entryOK = c:
    (c ? id) && (c ? repo) && (c ? ref)
    && lib.elem (c.trust or "untrusted") validTrust
    && !(lib.elem (c.ref or "") movingRefs)
    && lib.elem (c.policy or "strict") validPolicy
    && (!(c ? category) || lib.elem c.category validCategory);

  good = {
    id = "homelab-official";
    repo = "https://github.com/example/homelab-catalog";
    ref = "v1.0.0";
    trust = "official";
    policy = "strict";
    category = "media";
  };

  # The real, committed catalog must validate (empty is valid).
  realCatalogs = (import ../config/catalogs.nix).catalogs or [ ];

  checks = [
    { name = "good-entry"; ok = entryOK good; }
    { name = "missing-id"; ok = entryOK (removeAttrs good [ "id" ]) == false; }
    { name = "missing-repo"; ok = entryOK (removeAttrs good [ "repo" ]) == false; }
    { name = "moving-ref-main"; ok = entryOK (good // { ref = "main"; }) == false; }
    { name = "moving-ref-latest"; ok = entryOK (good // { ref = "latest"; }) == false; }
    { name = "bad-trust"; ok = entryOK (good // { trust = "random"; }) == false; }
    { name = "bad-policy"; ok = entryOK (good // { policy = "loose"; }) == false; }
    { name = "bad-category"; ok = entryOK (good // { category = "nope"; }) == false; }
    { name = "sha-ref-ok"; ok = entryOK (good // { ref = "0123456789abcdef0123456789abcdef01234567"; }); }
    { name = "no-policy-defaults-ok"; ok = entryOK (removeAttrs good [ "policy" ]); }
    { name = "real-catalogs-valid"; ok = lib.all entryOK realCatalogs; }
  ];

  failed = builtins.filter (c: !c.ok) checks;
in
if failed == [ ] then "ok"
else throw "catalog test failures: ${builtins.toJSON (map (c: c.name) failed)}"
