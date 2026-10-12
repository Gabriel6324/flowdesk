import {StrictMode,Component} from 'react';
import {createRoot} from 'react-dom/client';
import App from './App';
import './styles.css';
class Boundary extends Component<{children:React.ReactNode},{failed:boolean}> {
 state={failed:false};
 static getDerivedStateFromError(){return {failed:true}}
 render(){return this.state.failed?<div className="startup"><h1>页面暂时无法显示</h1><p>工单与已保存的记录仍在。请刷新页面重试。</p><button className="button primary" onClick={()=>location.reload()}>刷新页面</button></div>:this.props.children}
}
createRoot(document.getElementById('root')!).render(<StrictMode><Boundary><App/></Boundary></StrictMode>);

