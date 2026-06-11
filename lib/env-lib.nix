{ lib, env }:

rec {
  get = key: default: if env ? ${key} && env.${key} != "" then env.${key} else default;
  getBool = key: default: lib.toLower (get key (if default then "true" else "false")) == "true";
  getInt = key: default: lib.toInt (get key (toString default));
  getList = key: default:
    let v = get key ""; in
    if v == "" then default
    else lib.filter (s: s != "") (map lib.trim (lib.splitString "," v));
}
