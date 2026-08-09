import { c as createFlowDiagram, s as styles_default } from "./chunk-flowDiagram-UKHOOZJN.js";
import { _ as __name } from "./app.js";
import "./chunk-chunk-5VM5RSS4.js";
import "./chunk-chunk-XXDRQBXY.js";
import "./chunk-chunk-KBJHAD2P.js";
import "./chunk-chunk-2GRJ4B5K.js";
import "./chunk-channel.js";
var getStyles = /* @__PURE__ */ __name((options) => `${styles_default(options)}
  .swimlane.cluster rect {
    stroke: ${options.clusterBorder} !important;
  }
  [data-look="neo"].cluster rect {
    filter: none;
  }
`, "getStyles");
var styles_default2 = getStyles;
var diagram = createFlowDiagram({ defaultLayout: "swimlane", styles: styles_default2 });
export {
  diagram
};
