{ lib }:

path:

let
  raw = if path != "" && builtins.pathExists path then builtins.readFile path else "";
  lines = lib.splitString "\n" raw;
  stripExport = s: lib.removePrefix "export " s;
  isEntry = l:
    let t = lib.trim (stripExport l);
    in t != "" && !(lib.hasPrefix "#" t) && lib.hasInfix "=" t;
  unquote = v:
    let t = lib.trim v;
    in
      # Trim again after stripping quotes so stray whitespace inside the
      # quotes does not leak into values. Tradeoff: deliberately quoted
      # leading/trailing spaces are lost; .env consumers here never need them.
      if lib.hasPrefix "\"" t && lib.hasSuffix "\"" t then
        lib.trim (lib.removeSuffix "\"" (lib.removePrefix "\"" t))
      else if lib.hasPrefix "'" t && lib.hasSuffix "'" t then
        lib.trim (lib.removeSuffix "'" (lib.removePrefix "'" t))
      else t;
  toPair = l:
    let
      t = lib.trim (stripExport l);
      idx = builtins.head (builtins.match "([^=]*)=.*" t);
      key = lib.trim idx;
      value = unquote (lib.removePrefix "${idx}=" t);
    in lib.nameValuePair key value;
in
lib.listToAttrs (map toPair (lib.filter isEntry lines))
