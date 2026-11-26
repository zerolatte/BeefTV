import wordmark from "./assets/institute-wordmark.png";
import icon from "./assets/institute-icon.jpg";

/** 登录页与研究院工作区共用原始品牌资源，不经过通用 logo 的滤色处理。 */
export const instituteBrand = {
    name: "长钢研究院",
    latinName: "Changgang Research Institute",
    workbench: "影视创作工作台",
    wordmark,
    icon,
} as const;
