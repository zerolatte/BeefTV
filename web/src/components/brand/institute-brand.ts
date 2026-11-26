import wordmark from "./assets/institute-wordmark.png";
import icon from "./assets/institute-icon.jpg";

const name = "长钢研究院";
const workbench = "影视创作工作台";

/** 网页元信息、登录页与研究院工作区共用品牌文案和原色资源。 */
export const instituteBrand = {
    name,
    latinName: "Changgang Research Institute",
    workbench,
    title: `${name} · ${workbench}`,
    description: `${name}${workbench}，面向 AI 影视与短剧创作，集项目管理、智能生成与资产管理于一体。`,
    wordmark,
    icon,
} as const;
