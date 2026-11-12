// Optional panels are selected from bundled components. Older v1 remotes keep
// their existing state-based behavior when feature discovery is unavailable.
export function createCapabilities(root) {
  let features=null;
  const known=new Set(['front-eq','center-level','bass-offset','timer','messages','display-clock']);
  const bindings={'bass_offset_set':['bass-offset','set'],'eq_apply':['front-eq','apply'],'eq_reset':['front-eq','reset'],'center_next':['center-level','next'],'timer_start':['timer','start'],'timer_cancel':['timer','cancel'],'timer_extend':['timer','extend'],'display_message':['messages','show'],'display_cancel':['messages','cancel'],'display_clock':['display-clock','set']};
  function command(action,gains,value,parameters) {
    if(action==='av_sync_up' || action==='av_sync_down') return {action:'av_sync_set'};
    if(action==='av_sync_set') return {action,value};
    const binding=bindings[action];
    const feature=binding && features?.extensions.find(item=>item.component===binding[0] && item.included);
    const target=feature && feature.id+'.'+binding[1];
    if(target && feature.actions.includes(target)) return parameters ? {action:target,parameters} : action==='eq_apply' ? {action:target,parameters:{gains_db:gains}} : {action:target};
    return action==='eq_apply' ? {action,gains_db:gains} : {action};
  }
  function state(snapshot) {
    if(!snapshot || !features) return snapshot;
    let value=snapshot;
    for(const feature of features.extensions) {
      if(!feature.included || !known.has(feature.component)) continue;
      const projected=snapshot.extensions?.[feature.id];
      if(projected==null) continue;
      if(value===snapshot) value={...snapshot};
      if(feature.component==='bass-offset') value.bass_offset=projected;
      if(feature.component==='front-eq') value.eq=projected;
      if(feature.component==='timer') value.timers=projected.timers;
      if(feature.component==='messages') value.messages=projected.messages;
      if(feature.component==='display-clock') value.display_clock=projected;
      if(feature.component==='center-level') value.sound={...value.sound,center:projected.level,center_error:projected.error};
    }
    return value;
  }
  function apply() {
    if(!features) return;
    for(const element of root.querySelectorAll('[data-component]')) {
      const component=element.dataset.component;
      if(!known.has(component)) continue;
      element.hidden=!features.extensions.some(item=>item.component===component && item.included===true);
      if(element.hidden) for(const button of element.querySelectorAll('button')) button.disabled=true;
    }
    manual();
    const label=root.querySelector('#model-name');
    if(label) label.textContent=features.name;
    root.title=features.name+' · Remote';
    // A declared core action list permits read-only and smaller backends.
    const actions=new Set(features.actions);
    for(const item of features.extensions) if(item.included) for(const action of item.actions||[]) actions.add(action);
    // Existing v1 aliases remain advertised by the model composition.
    for(const button of root.querySelectorAll('[data-action]')) {
      if(!actions.has(command(button.dataset.action).action)) button.disabled=true;
    }
  }
  // The owner's manual the model declares, in the browser's language when
  // available, otherwise English. Links are JBL's own https PDFs.
  const languages={en:'English',fr:'Français',de:'Deutsch',es:'Español',nl:'Nederlands'};
  function manual() {
    const row=root.querySelector('#manual-row'), link=root.querySelector('#manual-link');
    if(!row || !link) return;
    const manuals=features.manuals && typeof features.manuals==='object' ? features.manuals : {};
    const valid=code=>typeof manuals[code]==='string' && /^https:\/\/[^\s"<>]+$/.test(manuals[code]);
    const preferred=[...(navigator.languages||[]),navigator.language,'en'].map(tag=>String(tag||'').slice(0,2).toLowerCase());
    const code=preferred.find(valid) || Object.keys(manuals).sort().find(valid);
    row.hidden=!code;
    if(!code) return;
    link.href=manuals[code];
    link.firstChild.textContent='PDF · '+(languages[code]||code.toUpperCase())+' ';
    link.title=features.name+' owner\'s manual (opens JBL\'s site)';
  }
  async function load() {
    try {
      const response=await fetch('/api/features',{cache:'no-store',signal:AbortSignal.timeout(4000)});
      if(!response.ok) return;
      const value=await response.json();
      if(value.schema!==1 || typeof value.model!=='string' || typeof value.name!=='string' ||
         !Array.isArray(value.actions) || !value.actions.every(a=>typeof a==='string') ||
         !Array.isArray(value.extensions) || !value.extensions.every(item=>item && typeof item.id==='string' && typeof item.included==='boolean' &&
           (item.component===undefined || typeof item.component==='string') && Array.isArray(item.actions) && item.actions.every(action=>typeof action==='string'))) return;
      features=value;
      apply();
    } catch { /* Existing v1 API remains usable; stream handles connectivity. */ }
  }
  return {load,apply,command,state,name:()=>features?.name || 'JBL Bar 9.1'};
}
