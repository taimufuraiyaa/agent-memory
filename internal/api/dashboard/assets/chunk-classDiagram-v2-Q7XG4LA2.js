import { s as styles_default, c as classRenderer_v3_unified_default, a as classDiagram_default, C as ClassDB } from "./chunk-chunk-727SXJPM.js";
import { _ as __name } from "./app.js";
import "./chunk-chunk-FMBD7UC4.js";
import "./chunk-chunk-ND2GUHAM.js";
import "./chunk-chunk-55IACEB6.js";
import "./chunk-chunk-2J33WTMH.js";
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
