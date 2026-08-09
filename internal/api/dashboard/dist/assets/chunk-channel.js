import { ag as Utils, ah as Color } from "./app.js";
const channel = (color, channel2) => {
  return Utils.lang.round(Color.parse(color)[channel2]);
};
export {
  channel as c
};
