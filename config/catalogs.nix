# Workshop catalog sources (HomeLab Platform V2).
#
# Each enabled catalog points at a community module library on GitHub, pinned
# to a ref, with a trust level and per-catalog policy. Consumed by
# modules/platform.nix (/etc/homelab/catalogs.json) and by the control-api
# library endpoint (/v1/library).
#
# Schema per entry (validated by modules/platform.nix + tests/catalog.nix):
#   id          required  [a-z0-9-], unique
#   repo        required  https:// git URL
#   ref         required  tag or 40-char commit SHA — never a moving branch
#   trust       required  official | community | untrusted
#   policy      optional  strict | warn          (default strict)
#   name        optional  human label for the UI
#   description optional  one line shown in the catalog list
#   category    optional  media | network | dev | data | monitoring | misc
{
  catalogs = [
    {
      id = "homelab-demo";
      repo = "https://github.com/Val-k7/homelab-demo-catalog";
      ref = "v1.0.2";
      trust = "community";
      policy = "warn";
      name = "HomeLab Demo Catalog";
      description = "Example modules (one per runner) for docs and UI demos.";
      category = "dev";
    }
  ];
}
