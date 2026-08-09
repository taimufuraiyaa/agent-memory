import { s as styles_default, c as classRenderer_v3_unified_default, a as classDiagram_default, C as ClassDB } from "./chunk-chunk-GF5L2VYU.js";
import { _ as __name } from "./app.js";
import "./chunk-chunk-5VM5RSS4.js";
import "./chunk-chunk-XXDRQBXY.js";
import "./chunk-chunk-KBJHAD2P.js";
import "./chunk-chunk-2GRJ4B5K.js";
var diagram = {
  parser: classDiagram_default,
  get db() {
    return new ClassDB();
  },
  renderer: classRenderer_v3_unified_default,
  styles: styles_default,
  init: /* @__PURE__ */ __name((cnf) => {
    if (!cnf.class) {
      cnf.class = {};
    }
    cnf.class.arrowMarkerAbsolute = cnf.arrowMarkerAbsolute;
  }, "init")
};
export {
  diagram
};
